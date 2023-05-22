package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/apex/log"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/costexplorer"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	"github.com/patrickmn/go-cache"
)

var (
	dataCache *cache.Cache
	cacheLock sync.Mutex
)

func generateFaviconHandler(c *gin.Context) {
	// Generate the favicon image
	faviconImage, err := generateFavicon()
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to generate favicon")
		return
	}

	// Create a buffer to write the favicon image
	faviconBuffer := new(bytes.Buffer)

	// Encode the favicon image as PNG format
	err = png.Encode(faviconBuffer, faviconImage)
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to encode favicon")
		return
	}

	// Set the response header for the favicon
	c.Header("Content-Type", "image/x-icon")

	// Write the favicon image buffer to the response
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Write(faviconBuffer.Bytes())
}

func generateFavicon() (image.Image, error) {
	// Create a new image for the favicon with a transparent background
	faviconImage := image.NewRGBA(image.Rect(0, 0, 16, 16))

	// Set the color for the golden circle
	goldenColor := color.RGBA{R: 255, G: 215, B: 0, A: 255}

	// Draw a filled golden circle on the favicon image
	drawCircle(faviconImage, 8, 8, 7, goldenColor)

	return faviconImage, nil
}

func drawCircle(img draw.Image, x, y, radius int, col color.RGBA) {
	// Iterate over the pixels in the circle and set the color
	for i := x - radius; i < x+radius; i++ {
		for j := y - radius; j < y+radius; j++ {
			if (i-x)*(i-x)+(j-y)*(j-y) < radius*radius {
				img.Set(i, j, col)
			}
		}
	}
}

func getWeeklyCost(c *gin.Context) {
	// Check if data is present in the cache
	cacheLock.Lock()
	cachedData, found := dataCache.Get("previous-four-weeks-data")
	cacheLock.Unlock()

	if found {
		c.JSON(http.StatusOK, cachedData)
		return
	}

	// Set up an AWS session
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(os.Getenv("AWS_DEFAULT_REGION")),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create AWS session"})
		return
	}

	svc := costexplorer.New(sess)

	// Calculate the start and end dates for the previous 4 weeks
	end := time.Now().AddDate(0, 0, -1) // End at yesterday
	start := end.AddDate(0, 0, -27)     // Start 4 weeks before yesterday

	// Make the API call to retrieve the cost and usage data
	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &costexplorer.DateInterval{
			Start: aws.String(start.Format("2006-01-02")),
			End:   aws.String(end.Format("2006-01-02")),
		},
		Granularity: aws.String("DAILY"),
		Metrics:     []*string{aws.String("BlendedCost")},
		GroupBy: []*costexplorer.GroupDefinition{
			{
				Type: aws.String("DIMENSION"),
				Key:  aws.String("SERVICE"),
			},
		},
		Filter: &costexplorer.Expression{
			And: []*costexplorer.Expression{
				{
					Dimensions: &costexplorer.DimensionValues{
						Key:    aws.String("RECORD_TYPE"),
						Values: []*string{aws.String("Usage")},
					},
				},
				{
					Not: &costexplorer.Expression{
						Dimensions: &costexplorer.DimensionValues{
							Key:    aws.String("USAGE_TYPE"),
							Values: []*string{aws.String("Credit"), aws.String("Refund")},
						},
					},
				},
			},
		},
	}

	log.Info("getWeeklyCost: requesting data to AWS")
	output, err := svc.GetCostAndUsage(input)
	if err != nil {
		response := gin.H{
			"error": "Failed to retrieve cost and usage data",
			"start": start.String(),
			"end":   end.String(),
		}
		if os.Getenv("GIN_MODE") != "release" {
			response["exception"] = err.Error()
		}

		c.JSON(http.StatusInternalServerError, response)
		return
	}

	log.Debugf("getWeeklyCost: GetCostAndUsage output: %+v", output)

	// Process the API response and build the result
	result := make(map[string]map[string]float64)
	categories := make(map[string]bool)

	// Retrieve all unique categories
	for _, dayData := range output.ResultsByTime {
		for _, group := range dayData.Groups {
			category := *group.Keys[0]
			categories[category] = true
		}
	}

	// Initialize categories for each day with 0 values
	for _, dayData := range output.ResultsByTime {
		day := *dayData.TimePeriod.Start
		result[day] = make(map[string]float64)
		for category := range categories {
			result[day][category] = 0
		}
	}

	// Update the result with actual data
	for _, dayData := range output.ResultsByTime {
		day := *dayData.TimePeriod.Start
		for _, group := range dayData.Groups {
			category := *group.Keys[0]
			amountFloat, err := strconv.ParseFloat(*group.Metrics["BlendedCost"].Amount, 64)
			if err == nil {
				result[day][category] = amountFloat
			}
		}
	}

	// Cache the result
	cacheLock.Lock()
	dataCache.Set("previous-four-weeks-data", result, cache.DefaultExpiration)
	cacheLock.Unlock()

	c.JSON(http.StatusOK, result)
}

func getMonthlyCost(c *gin.Context) {
	// Check if data is present in the cache
	cacheLock.Lock()
	cachedData, found := dataCache.Get("monthly-cost")
	cacheLock.Unlock()

	if found {
		c.JSON(http.StatusOK, cachedData)
		return
	}

	// Set up an AWS session
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(os.Getenv("AWS_DEFAULT_REGION")),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create AWS session"})
		return
	}

	svc := costexplorer.New(sess)

	// Calculate the start and end dates for the previous year
	now := time.Now()
	end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Add(-time.Nanosecond)
	start := end.AddDate(0, -12, 0)

	// Make the API call to retrieve the cost and usage data
	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &costexplorer.DateInterval{
			Start: aws.String(start.Format("2006-01-02")),
			End:   aws.String(end.Format("2006-01-02")),
		},
		Granularity: aws.String("MONTHLY"),
		Metrics:     []*string{aws.String("BlendedCost")},
		GroupBy: []*costexplorer.GroupDefinition{
			{
				Type: aws.String("DIMENSION"),
				Key:  aws.String("SERVICE"),
			},
		},
		Filter: &costexplorer.Expression{
			And: []*costexplorer.Expression{
				{
					Dimensions: &costexplorer.DimensionValues{
						Key:    aws.String("RECORD_TYPE"),
						Values: []*string{aws.String("Usage")},
					},
				},
				{
					Not: &costexplorer.Expression{
						Dimensions: &costexplorer.DimensionValues{
							Key:    aws.String("USAGE_TYPE"),
							Values: []*string{aws.String("Credit"), aws.String("Refund")},
						},
					},
				},
			},
		},
	}

	log.Info("getMonthlyCost: requesting data to AWS")
	output, err := svc.GetCostAndUsage(input)
	if err != nil {
		response := gin.H{
			"error": "Failed to retrieve cost and usage data",
			"start": start.String(),
			"end":   end.String(),
		}
		if os.Getenv("GIN_MODE") != "release" {
			response["exception"] = err.Error()
		}

		c.JSON(http.StatusInternalServerError, response)
		return
	}

	log.Debugf("GetCostAndUsage output: %+v", output)

	// Process the API response and build the result
	result := make(map[string]map[string]float64)
	categories := make(map[string]bool)

	// Retrieve all unique categories
	for _, dayData := range output.ResultsByTime {
		for _, group := range dayData.Groups {
			category := *group.Keys[0]
			categories[category] = true
		}
	}

	// Initialize categories for each day with 0 values
	for _, dayData := range output.ResultsByTime {
		day := *dayData.TimePeriod.Start
		result[day] = make(map[string]float64)
		for category := range categories {
			result[day][category] = 0
		}
	}

	// Update the result with actual data
	for _, dayData := range output.ResultsByTime {
		day := *dayData.TimePeriod.Start
		for _, group := range dayData.Groups {
			category := *group.Keys[0]
			amountFloat, err := strconv.ParseFloat(*group.Metrics["BlendedCost"].Amount, 64)
			if err == nil {
				result[day][category] = amountFloat
			}
		}
	}

	// Cache the result
	cacheLock.Lock()
	dataCache.Set("monthly-cost", result, cache.DefaultExpiration)
	cacheLock.Unlock()

	c.JSON(http.StatusOK, result)
}

func main() {
	dataCache = cache.New(24*time.Hour, 24*time.Hour)

	// Set the log level based on the GIN_MODE environment variable
	if os.Getenv("GIN_MODE") == "release" {
		log.SetLevel(log.InfoLevel)
	} else {
		log.SetLevel(log.DebugLevel)
	}

	r := gin.Default()
	r.GET("/monthly-cost.json", getMonthlyCost)
	r.GET("/weekly-cost.json", getWeeklyCost)

	r.GET("/favicon.ico", generateFaviconHandler)

	r.Use(static.Serve("/", static.LocalFile("./public", true)))
	r.Run(":8080")
}

package main

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/apex/log"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/costexplorer"
	"github.com/gin-gonic/gin"
	"github.com/patrickmn/go-cache"
)

var (
	dataCache *cache.Cache
	cacheLock sync.Mutex
)

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
		Region: aws.String("us-west-2"), // Replace with your desired AWS region
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create AWS session"})
		return
	}

	svc := costexplorer.New(sess)

	// Calculate the start and end dates for the previous year
	now := time.Now()
	end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Add(-time.Nanosecond)
	start := end.AddDate(0, -6, 0)

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
	}

	output, err := svc.GetCostAndUsage(input)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":     "Failed to retrieve cost and usage data",
			"exception": err.Error(),
			"start":     start.String(),
			"end":       end.String(),
		})
		return
	}

	log.Debugf("GetCostAndUsage output: %+v", output)

	// Process the API response and build the result
	result := make(map[string]map[string]float64)
	for _, monthData := range output.ResultsByTime {
		month := *monthData.TimePeriod.Start
		result[month] = make(map[string]float64)
		for _, group := range monthData.Groups {
			category := *group.Keys[0]
			amount_float, err := strconv.ParseFloat(*group.Metrics["BlendedCost"].Amount, 64)
			if err == nil {
				result[month][category] = amount_float
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
	dataCache = cache.New(24*time.Hour, 1*time.Hour) // Cache data for 24 hours, refresh every 1 hour

	log.SetLevel(log.DebugLevel)

	r := gin.Default()
	r.GET("/monthly-cost", getMonthlyCost)
	r.Run(":8080")
}

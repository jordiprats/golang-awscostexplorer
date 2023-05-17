all: clean webcost

.PHONY: clean
clean:
	rm -f webcost

webcost:
	go mod download
	go build -a -o webcost main.go
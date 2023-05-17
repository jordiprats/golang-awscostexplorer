all: clean awscost

.PHONY: clean
clean:
	rm -f awscost

awscost:
	go mod download
	go build -a -o awscost main.go
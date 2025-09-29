BINARY_NAME=aurora
GOOS=linux
GOARCH=amd64

.PHONY: all build clean run

all: build

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(BINARY_NAME) .

clean:
	rm -f $(BINARY_NAME)

run: build
	./$(BINARY_NAME)

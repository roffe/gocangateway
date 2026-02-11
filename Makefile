.PHONY: clean

default: cangateway

cangateway:
	go build -tags="j2534" -ldflags '-s -w' -o cangateway .

clean:
	rm -f cangateway
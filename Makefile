.PHONY: run test race

run:
	go run ./cmd/forgehttp

test:
	go test ./...

race:
	go test -race ./...

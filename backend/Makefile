.PHONY: generate test test-integration vet
 generate:
	go tool oapi-codegen -config oapi-codegen.yaml ../docs/api/openapi.yaml
	go tool sqlc generate
 test:
	go test ./... -count=1
 test-integration:
	go test ./... -count=1 -race
 vet:
	go vet ./...

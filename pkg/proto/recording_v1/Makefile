# pkg/proto/recording_v1/Makefile

.PHONY: gen

gen:
	protoc --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    recording.proto
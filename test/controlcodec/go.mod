module github.com/tsukiyoz/resona/test/controlcodec

go 1.26.0

require (
	github.com/fxamacker/cbor/v2 v2.9.3
	github.com/tsukiyoz/resona v0.0.0
)

require (
	github.com/flynn/noise v1.1.0 // indirect
	github.com/quic-go/quic-go v0.62.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/tsukiyoz/resona => ../..

replace github.com/honeybbq/teamspeak-go => ../../third_party/teamspeak-go

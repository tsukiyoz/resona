module github.com/tsukiyoz/resona

go 1.26.0

require (
	github.com/gen2brain/malgo v0.11.26
	github.com/honeybbq/teamspeak-go v0.0.0
	github.com/keybase/go-keychain v0.0.1
	github.com/thesyncim/gopus v0.1.1
	golang.org/x/image v0.41.0
	golang.org/x/sys v0.46.0
)

replace github.com/honeybbq/teamspeak-go => ./third_party/teamspeak-go

require (
	github.com/oasisprotocol/curve25519-voi v0.0.0-20251114093237-2ab5a27a1729 // indirect
	github.com/tink-crypto/tink-go/v2 v2.8.0 // indirect
	golang.org/x/crypto v0.53.0 // indirect
)

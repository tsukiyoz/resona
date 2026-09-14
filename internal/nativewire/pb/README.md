# Control message generation

`control.proto` is the schema. `control.pb.go` is generated and checked into Git;
normal app/server builds need neither protoc nor a generation step.

To regenerate from the repository root:

```sh
go generate ./internal/nativewire/pb
```

`make generate` runs the same command. Install protoc 29.3 on PATH. The generator
checks that version and builds protoc-gen-go into a temporary directory using
the protobuf module pinned in the root go.mod (currently v1.36.12). It does not
depend on an arbitrary globally installed plugin. The Go entry point works on
Windows and macOS/Linux; temporary plugin files are removed afterward.

Do not edit generated code. Regenerate after schema changes and review both
files. Never reuse field numbers; reserve deleted numbers and names. Current
VoiceState commands set both booleans, with absent values meaning false. Future
partial-update messages must use presence-aware fields or a separate message.

Envelope kind selects the body schema. Unknown fields in known messages are
ignored at this endpoint boundary, not preserved through application snapshots.
New commands or changed semantics still need a compatibility/capability decision.
See [wire specification](../../../docs/native-protocol.md).

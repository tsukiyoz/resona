# Control codec comparison

This independent Go module keeps the former CBOR codec in `baseline/` solely
for reproducible measurements. It is not a product dependency or compatibility
fallback. The root product module no longer requires CBOR.

Run from this directory:

```sh
go test -v -run TestSizesAndRoundTrips -bench BenchmarkControlCodec -benchmem -benchtime=200ms -count=3 ./...
```

The benchmark measures complete Pack and Read+Decode, including the four-byte
length, envelope, allocations and mapping to ordinary application values.
Fixtures round-trip through both codecs. It does not time network I/O, crypto,
Opus, server locks, or cross-language calls.

## 2026-09-14 local sample

Apple M2, macOS ARM64, Go 1.26. Each time is the median of three 200 ms runs in ns.
Desktop/background scheduling introduces noise; these are local samples, not a
statistically established production improvement. Both codecs run sequentially
within the same benchmark process, with initialized metadata caches.

| Fixture | CBOR bytes | Protobuf bytes | CBOR encode ns | Protobuf encode ns | CBOR decode ns | Protobuf decode ns |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| hello | 22 | 24 | 305.5 | 221.8 | 498.2 | 285.8 |
| move | 12 | 12 | 213.5 | 187 | 431.3 | 238.9 |
| voice_state | 12 | 12 | 211.7 | 189.2 | 428.2 | 241.4 |
| reply_ok | 9 | 8 | 161.8 | 159.2 | 312.1 | 185.6 |
| chat | 32 | 36 | 217.5 | 223.9 | 461.4 | 300.4 |
| state4 | 351 | 383 | 1205 | 1073 | 2777 | 1653 |
| state64 | 3393 | 3603 | 9622 | 8277 | 22499 | 11225 |

Memory allocated per operation (B/op, not peak process memory):

| Fixture / operation | CBOR B/op | Protobuf B/op | CBOR allocations | Protobuf allocations |
| --- | ---: | ---: | ---: | ---: |
| move / encode | 64 | 176 | 4 | 4 |
| move / decode | 136 | 264 | 6 | 7 |
| state4 / encode | 1088 | 1696 | 4 | 8 |
| state4 / decode | 1664 | 2656 | 27 | 40 |
| state64 / encode | 10404 | 15392 | 4 | 8 |
| state64 / decode | 14240 | 23168 | 147 | 224 |

Protobuf decodes these fixtures faster, but is not universally smaller or more
allocation-efficient. The 64-member packet is about 6.2% larger and decode
allocations increase from 147 to 224. Its encoder uses contiguous temporary
message arrays: eight allocations rather than the initial one-message-per-member
implementation's 74. It still allocates more bytes than the CBOR baseline.

The old format is positional CBOR arrays, with no field names/tags per member
field. Protobuf spends bytes on field numbers and nested lengths while omitting
default scalar values. Results depend on field presence, numeric values and
string sizes. Snapshot fixtures use four channels and 4/64 members with bounded
synthetic names/instance strings. These are not actual users or credentials.

The migration is accepted for explicit schema, generated tooling and compatible
field evolution, with the measured decode benefit. It is not evidence that the
whole server now uses less CPU or memory. Further tuning must preserve ordinary
application-state ownership, numeric/collection bounds and unknown-field behavior.

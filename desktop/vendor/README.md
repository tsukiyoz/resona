# Pinned compatibility patches

Cargo's desktop-level patches apply to all transitive users of these packages.
These are source fixes, not warning suppression or build-time registry edits.
Remove each patch after an evaluated upstream release contains the fix.

## GPUI 0.2.2

See [its patch record](gpui/RESONA-PATCH.md).

## block 0.1.6

Source: crates.io package checksum
`0d8c1fef690941d3e7788d328517591fecc684c084084702d6ff1641e993699a`,
<https://github.com/SSheldon/rust-block>. The package declares MIT licensing;
its published archive does not include a separate license file.

`src/lib.rs`: represent the opaque `Class` as an inhabited zero-sized C-layout
struct instead of an empty enum. `_NSConcreteStackBlock` is a foreign symbol
whose address is used as the block's class pointer; declaring it with an
uninhabited type triggers Rust's `uninhabited_static` future incompatibility.
The pointer layout and block ownership operations are unchanged.
Spell the existing implicit C ABI explicitly as `extern "C"` throughout the
same file, addressing Rust 1.99's `missing_abi` deprecation without changing ABI.

The archive's development-only `objc_test_utils` path is not published; remove
that unusable dependency from the local manifest. Desktop integration tests
exercise block copying, cloning and invocation on macOS.

## proc-macro-error2 2.0.1

Source: crates.io package checksum
`11ec05c52be0a07b08061f7dd003e7d7092e0472bc731b4af7bb1ef876109802`,
<https://github.com/GnomedDev/proc-macro-error-2>.
The original MIT and Apache 2.0 license files are retained.

`src/lib.rs`: make `extern crate proc_macro` public, matching its existing public
re-export in `__export`. This fixes `pub_use_of_private_extern_crate` without
changing the procedural macro implementation. The package's existing tests are
retained. No dependency version is upgraded.

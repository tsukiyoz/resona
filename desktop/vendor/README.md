# Pinned compatibility patches

Cargo's desktop-level patches apply to all transitive users of these packages.
These are source fixes, not warning suppression or build-time registry edits.
Remove each patch after an evaluated upstream release contains the fix.

## gpui-pre-windows 0.3.10

Source: crates.io package checksum
`80670f987b920fba019917b2e222ecf2b377f2a6d77872010c7d284c235837b3`,
<https://github.com/zed-industries/zed>, upstream Zed commit
`08cbb88a152c3751a43e853425a70e9f631a5918` (published package metadata).
The original Apache 2.0 license is retained and included in desktop packages.

`src/events.rs`: apply the Windows system titlebar appearance before invoking
the application appearance callback. Resona can then keep its explicit light/dark
preference when Windows changes appearance. The callback still sees the true system
appearance for the "follow system" preference. No timer or extra repaint is added.

The previous full `gpui` 0.2.2 vendor tree is retired with the move to Kit 0.8.1's
default `gpui-pre` 0.3.10 backend. Its macOS focus-lock fix is present upstream in
`gpui-pre-macos/src/window.rs` (`drop(lock)` before `resignKeyWindow`). The former
Taffy float literal patch is no longer needed by the new dependency graph.

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

The Kit backend still depends on this package through Metal, so the patch and its
macOS integration test remain. The old `proc-macro-error2` patch is removed because
that package is absent from the Kit dependency graph.

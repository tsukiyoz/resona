fn main() {
    println!("cargo:rerun-if-changed=../VERSION");
    let version = std::fs::read_to_string("../VERSION").expect("read root VERSION");
    assert_eq!(
        version.trim(),
        std::env::var("CARGO_PKG_VERSION").unwrap(),
        "desktop/Cargo.toml must match root VERSION"
    );
    println!("cargo:rerun-if-changed=assets/resona.ico");
    println!("cargo:rerun-if-changed=assets/resona.rc");
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("windows") {
        // GPUI loads application icon resource 1 for the native window class.
        embed_resource::compile("assets/resona.rc", embed_resource::NONE)
            .manifest_required()
            .expect("failed to embed Resona application icon");
    }
}

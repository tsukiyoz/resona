fn main() {
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("windows") {
        // Bundled WebRTC uses timeGetTime; keep this after its static archives.
        println!("cargo:rustc-link-arg=-lwinmm");
    }
}

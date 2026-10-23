fn main() -> Result<(), Box<dyn std::error::Error>> {
    println!("cargo:rerun-if-changed=../VERSION");
    assert_eq!(
        std::fs::read_to_string("../VERSION")?.trim(),
        std::env::var("CARGO_PKG_VERSION")?,
        "client/Cargo.toml must match root VERSION"
    );
    let compiler =
        std::process::Command::new(std::env::var_os("RUSTC").unwrap_or_else(|| "rustc".into()))
            .arg("--version")
            .output()?;
    println!(
        "cargo:rustc-env=RESONA_RUST_VERSION={}",
        String::from_utf8_lossy(&compiler.stdout).trim()
    );
    for (key, fallback) in [
        (
            "RESONA_VERSION",
            format!("v{}", std::env::var("CARGO_PKG_VERSION")?),
        ),
        ("RESONA_COMMIT", git(&["rev-parse", "--short=12", "HEAD"])),
        ("RESONA_BUILD_TIME", String::from("unknown")),
        (
            "RESONA_DIRTY",
            if git(&["status", "--porcelain"]).is_empty() {
                "false"
            } else {
                "true"
            }
            .into(),
        ),
        ("RESONA_UPDATE_URL", String::new()),
        ("RESONA_UPDATE_PUBLIC_KEY", String::new()),
    ] {
        println!("cargo:rerun-if-env-changed={key}");
        let value = std::env::var(key).unwrap_or(fallback);
        assert!(!value.contains(['\r', '\n']), "invalid build metadata");
        println!("cargo:rustc-env={key}={value}");
    }
    println!("cargo:rerun-if-changed=../.git/HEAD");
    println!("cargo:rerun-if-changed=../.git/index");
    if std::env::var_os("CARGO_FEATURE_AUDIO").is_some() {
        pkg_config::Config::new()
            .atleast_version("1.6.1")
            .probe("opus")?;
        let speex = "../native/speexdsp";
        let mut c = cc::Build::new();
        c.include(format!("{speex}/include"))
            .define("FLOATING_POINT", None)
            .define("USE_SMALLFT", None)
            .define("EXPORT", Some(""));
        for name in [
            "mdf.c",
            "preprocess.c",
            "smallft.c",
            "fftwrap.c",
            "filterbank.c",
        ] {
            c.file(format!("{speex}/{name}"));
        }
        c.warnings(false).compile("resona_speexdsp");
        println!("cargo:rerun-if-changed={speex}");
        cc::Build::new()
            .file("../native/client-audio/device.c")
            .warnings(false)
            .compile("resona_device");
        println!("cargo:rerun-if-changed=../native/client-audio");
        match std::env::var("CARGO_CFG_TARGET_OS")?.as_str() {
            "macos" => {
                for framework in ["CoreAudio", "AudioToolbox", "CoreFoundation"] {
                    println!("cargo:rustc-link-lib=framework={framework}");
                }
            }
            "linux" => {
                println!("cargo:rustc-link-lib=dl");
                println!("cargo:rustc-link-lib=pthread");
                println!("cargo:rustc-link-lib=m");
            }
            "windows" => {
                for name in ["ole32", "uuid", "avrt"] {
                    println!("cargo:rustc-link-lib={name}");
                }
            }
            _ => {}
        }
    }
    let schema = "../internal/nativewire/pb/control.proto";
    println!("cargo:rerun-if-changed={schema}");
    let mut config = prost_build::Config::new();
    config.protoc_executable(protoc_bin_vendored::protoc_bin_path()?);
    config.compile_protos(&[schema], &["../internal/nativewire/pb"])?;
    Ok(())
}
fn git(args: &[&str]) -> String {
    std::process::Command::new("git")
        .args(args)
        .output()
        .ok()
        .filter(|o| o.status.success())
        .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_owned())
        .unwrap_or_default()
}

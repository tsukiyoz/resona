fn main() -> Result<(), Box<dyn std::error::Error>> {
    if std::env::var_os("CARGO_FEATURE_AUDIO").is_some() {
        pkg_config::Config::new()
            .atleast_version("1.6.1")
            .probe("opus")?;
        let speex = "../internal/audio/speexdsp";
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

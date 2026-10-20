use std::{env, path::PathBuf};

fn main() {
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("windows") {
        // Bundled WebRTC uses timeGetTime; keep this after its static archives.
        println!("cargo:rustc-link-arg=-lwinmm");
    }
    println!("cargo:rerun-if-env-changed=RNNOISE_SOURCE");
    let source = env::var_os("RNNOISE_SOURCE")
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from("../../build/native/rnnoise"));
    assert!(
        source.join("src/rnnoise_data.c").is_file(),
        "run bash native/rnnoise/prepare.sh before building the native audio library"
    );
    let mut r = cc::Build::new();
    r.include(source.join("include"))
        .include(source.join("src"))
        .define("RNNOISE_BUILD", None);
    for name in [
        "denoise.c",
        "rnn.c",
        "pitch.c",
        "kiss_fft.c",
        "celt_lpc.c",
        "nnet.c",
        "nnet_default.c",
        "parse_lpcnet_weights.c",
        "rnnoise_tables.c",
        "rnnoise_data.c",
    ] {
        r.file(source.join("src").join(name));
    }
    if env::var("CARGO_CFG_TARGET_ARCH").unwrap() == "x86_64" {
        r.define("RNN_ENABLE_X86_RTCD", "1")
            .define("CPU_INFO_BY_ASM", "1")
            .file(source.join("src/x86/x86cpu.c"))
            .file(source.join("src/x86/x86_dnn_map.c"));
        for (file, flags) in [
            ("nnet_sse4_1.c", vec!["-msse4.1"]),
            ("nnet_avx2.c", vec!["-mavx", "-mfma", "-mavx2"]),
        ] {
            let mut simd = cc::Build::new();
            simd.include(source.join("include"))
                .include(source.join("src"))
                .define("RNN_ENABLE_X86_RTCD", "1")
                .warnings(false)
                .file(source.join("src/x86").join(file));
            for flag in flags {
                simd.flag(flag);
            }
            r.objects(simd.compile_intermediates());
        }
    }
    r.warnings(false).compile("resona_rnnoise");
    println!("cargo:rerun-if-changed={}", source.display());
}

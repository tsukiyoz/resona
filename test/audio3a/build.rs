use std::{env, path::PathBuf};

fn main() {
    if env::var("CARGO_CFG_TARGET_OS").unwrap() == "windows" {
        // Keep this after the dependency archives for MinGW's one-pass linker.
        println!("cargo:rustc-link-arg=-lwinmm");
    }
    let speex = PathBuf::from("../../internal/audio/speexdsp");
    let mut c = cc::Build::new();
    c.include(speex.join("include"))
        .include(&speex)
        .define("FLOATING_POINT", None)
        .define("USE_SMALLFT", None)
        .define("EXPORT", "")
        .file("native.c");
    for name in [
        "mdf.c",
        "preprocess.c",
        "fftwrap.c",
        "filterbank.c",
        "smallft.c",
    ] {
        c.file(speex.join(name));
    }
    c.warnings(false).compile("bench_speex");
    println!("cargo:rerun-if-changed=native.c");
    println!("cargo:rerun-if-changed={}", speex.display());
    println!("cargo:rerun-if-env-changed=RNNOISE_SOURCE");
    let source = PathBuf::from(
        env::var("RNNOISE_SOURCE").expect("run prepare.sh first; set RNNOISE_SOURCE"),
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
    ] {
        r.file(source.join("src").join(name));
    }
    // Keep the dispatcher portable; compile SIMD kernels separately as upstream does.
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
    let model = if env::var_os("CARGO_FEATURE_RNNOISE_LITTLE").is_some() {
        "rnnoise_data_little.c"
    } else {
        "rnnoise_data.c"
    };
    r.file(source.join("src").join(model))
        .warnings(false)
        .compile("bench_rnnoise");
    println!("cargo:rerun-if-changed={}", source.display());
}

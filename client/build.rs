fn main() -> Result<(), Box<dyn std::error::Error>> {
    let schema = "../internal/nativewire/pb/control.proto";
    println!("cargo:rerun-if-changed={schema}");
    let mut config = prost_build::Config::new();
    config.protoc_executable(protoc_bin_vendored::protoc_bin_path()?);
    config.compile_protos(&[schema], &["../internal/nativewire/pb"])?;
    Ok(())
}

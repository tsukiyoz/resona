use anyhow::{Context, Result, bail, ensure};
use resona_listening_bot::{Bots, LocalServer, read_pcm, server_binary};
use std::{path::PathBuf, time::Duration};
use tokio_util::sync::CancellationToken;

fn duration(value: &str) -> Result<Duration> {
    let (number, scale) = match value.as_bytes().last() {
        Some(b's') => (&value[..value.len() - 1], 1),
        Some(b'm') => (&value[..value.len() - 1], 60),
        Some(b'h') => (&value[..value.len() - 1], 3600),
        _ => (value, 1),
    };
    let seconds = number
        .parse::<u64>()?
        .checked_mul(scale)
        .context("duration overflow")?;
    ensure!(
        (1..=86400).contains(&seconds),
        "duration must be 1 second to 24 hours"
    );
    Ok(Duration::from_secs(seconds))
}

#[tokio::main(flavor = "current_thread")]
async fn main() -> Result<()> {
    let mut pcm = PathBuf::from("build/listening/noisy.f32");
    let mut agc = PathBuf::from("build/listening/agc.f32");
    let mut server = server_binary();
    let mut stop_after = Duration::from_secs(7200);
    let mut args = std::env::args().skip(1);
    while let Some(key) = args.next() {
        if matches!(key.as_str(), "--help" | "-h") {
            println!(
                "resona-listening-bot [--pcm FILE] [--agc-pcm FILE] [--duration 2h] [--server-binary FILE]"
            );
            return Ok(());
        }
        let value = args.next().context("option value required")?;
        match key.trim_start_matches('-') {
            "pcm" => pcm = value.into(),
            "agc-pcm" => agc = value.into(),
            "duration" => stop_after = duration(&value)?,
            "server-binary" => server = value.into(),
            _ => bail!("unknown option: {key}"),
        }
    }
    let (pcm, agc) = (read_pcm(&pcm)?, read_pcm(&agc)?);
    let mut server = LocalServer::start(&server).await?;
    let mut bots = Bots::start(&server.profile, &pcm, &agc).await?;
    println!(
        "Address: {}\nPublic key: {}\nPassword: none\nLoop: {:.1}s; stop after {:?} or Ctrl-C",
        server.profile.address,
        server.profile.server_public_key,
        pcm.len() as f64 / 48_000.0,
        stop_after
    );
    let cancel = CancellationToken::new();
    let result = tokio::select! {
        result = bots.run(&cancel) => result,
        result = tokio::signal::ctrl_c() => result.map_err(Into::into),
        _ = tokio::time::sleep(stop_after) => Ok(()),
    };
    cancel.cancel();
    bots.close().await;
    let stopped = server.stop().await;
    result?;
    stopped
}

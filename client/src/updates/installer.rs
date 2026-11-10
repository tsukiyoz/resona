//! Velopack owns installation layout, replacement and relaunch, never configuration.
use super::Asset;
use std::path::Path;

/// Handle install/update hooks before GPUI, devices or networking are initialized.
pub fn bootstrap() {
    #[cfg(any(target_os = "windows", target_os = "macos"))]
    velopack::VelopackApp::build()
        .set_auto_apply_on_startup(false)
        .run();
}

#[cfg(target_os = "windows")]
fn locate(current: &str) -> Result<velopack::locator::VelopackLocator, String> {
    use velopack::locator::{LocationContext, auto_locate_app_manifest};
    let locator = auto_locate_app_manifest(LocationContext::FromCurrentExe)
        .map_err(|_| "未找到有效的安装目录，请重新安装 Resona")?;
    if locator.get_is_portable()
        || locator.get_manifest_id() != "Resona"
        || locator.get_manifest_version_full_string() != current.trim_start_matches('v')
    {
        return Err("安装身份与当前版本不匹配，请重新安装 Resona".into());
    }
    Ok(locator)
}

pub(super) fn validate(current: &str) -> Result<(), String> {
    #[cfg(target_os = "windows")]
    {
        locate(current).map(|_| ())
    }
    #[cfg(not(target_os = "windows"))]
    {
        let _ = current;
        Err("此平台暂不提供应用内安装".into())
    }
}

pub(super) fn apply(
    current: &str,
    version: &str,
    asset: &Asset,
    path: &Path,
) -> Result<(), String> {
    #[cfg(target_os = "windows")]
    {
        let locator = locate(current)?;
        let manager = velopack::UpdateManager::new(velopack::sources::NoneSource {}, None, None)
            .map_err(|_| "无法初始化更新程序，请重新安装 Resona")?;
        let name = format!("Resona-{}-full.nupkg", version.trim_start_matches('v'));
        let destination = locator.get_packages_dir().join(&name);
        std::fs::create_dir_all(locator.get_packages_dir()).map_err(|_| "无法访问安装缓存")?;
        let mut source = std::fs::File::open(path).map_err(|_| "无法读取更新包")?;
        let mut staged = tempfile::NamedTempFile::new_in(locator.get_packages_dir())
            .map_err(|_| "无法创建安装缓存，请检查磁盘空间")?;
        std::io::copy(&mut source, &mut staged).map_err(|_| "复制更新包失败，请检查磁盘空间")?;
        staged
            .as_file()
            .sync_all()
            .map_err(|_| "保存安装缓存失败")?;
        super::validate_file(staged.path(), asset)?;
        staged
            .persist(&destination)
            .map_err(|_| "无法保存安装缓存，请重试")?;
        let release = velopack::VelopackAsset {
            PackageId: "Resona".into(),
            Version: version.trim_start_matches('v').into(),
            Type: "Full".into(),
            FileName: name,
            SHA256: asset.digest.clone(),
            Size: asset.size,
            ..Default::default()
        };
        manager
            .wait_exit_then_apply_updates(&release, false, true, Vec::<String>::new())
            .map_err(|_| "无法启动更新程序，请检查权限后重试".to_owned())
    }
    #[cfg(not(target_os = "windows"))]
    {
        let _ = (current, version, asset, path);
        Err("此平台暂不提供应用内安装".into())
    }
}

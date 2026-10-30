#![cfg_attr(all(windows, not(debug_assertions)), windows_subsystem = "windows")]

mod app_icon;
mod core;
mod model;
mod preferences;
mod theme;
mod ui;

use anyhow::Result;
use gpui_kit::{
    AppContext, AssetSource, Bounds, KeyBinding, SharedString, WindowBounds, WindowOptions, px,
    size,
};
use std::borrow::Cow;

struct Assets;

impl AssetSource for Assets {
    fn load(&self, path: &str) -> Result<Option<Cow<'static, [u8]>>> {
        let asset: Option<&'static [u8]> = match path {
            "resona.svg" => Some(include_bytes!("../assets/resona.svg")),
            "mic.svg" => Some(include_bytes!("../assets/mic.svg")),
            "mic-off.svg" => Some(include_bytes!("../assets/mic-off.svg")),
            "headphones.svg" => Some(include_bytes!("../assets/headphones.svg")),
            "headphones-off.svg" => Some(include_bytes!("../assets/headphones-off.svg")),
            "volume-2.svg" => Some(include_bytes!("../assets/volume-2.svg")),
            "smile.svg" => Some(include_bytes!("../assets/smile.svg")),
            _ => None,
        };
        if let Some(asset) = asset {
            return Ok(Some(Cow::Borrowed(asset)));
        }
        gpui_kit::assets::Assets.load(path)
    }

    fn list(&self, path: &str) -> Result<Vec<SharedString>> {
        let mut assets = gpui_kit::assets::Assets.list(path)?;
        for asset in [
            "resona.svg",
            "mic.svg",
            "mic-off.svg",
            "headphones.svg",
            "headphones-off.svg",
            "smile.svg",
            "volume-2.svg",
        ] {
            if asset.starts_with(path) {
                assets.push(asset.into());
            }
        }
        Ok(assets)
    }
}

fn main() -> Result<()> {
    match std::env::args().nth(1).as_deref() {
        Some("--version-json") => {
            println!(
                "{}",
                serde_json::to_string(&resona_client::build_info::BuildInfo::default())?
            );
            return Ok(());
        }
        Some("--version" | "-V") => {
            let info = resona_client::build_info::BuildInfo::default();
            println!(
                "resona-desktop {} ({}; {}; {})",
                info.version, info.commit, info.platform, info.rust_version
            );
            return Ok(());
        }
        _ => {}
    }
    gpui_kit::application().with_assets(Assets).run(|cx| {
        gpui_kit::init(cx);
        cx.bind_keys([
            KeyBinding::new("escape", ui::DismissEmoji, Some("ResonaEmoji")),
            KeyBinding::new("escape", ui::DismissModal, None),
            KeyBinding::new("cmd-q", ui::Quit, None),
            KeyBinding::new("ctrl-q", ui::Quit, None),
        ]);
        let bounds = Bounds::centered(None, size(px(800.), px(900.)), cx);
        cx.on_window_closed(|cx, _| {
            if cx.windows().is_empty() {
                cx.quit();
            }
        })
        .detach();
        gpui_kit::open_window(
            WindowOptions {
                titlebar: Some(gpui_kit::TitlebarOptions {
                    title: Some(concat!("Resona v", env!("CARGO_PKG_VERSION")).into()),
                    ..if cfg!(target_os = "macos") {
                        gpui_kit::component::TitleBar::title_bar_options()
                    } else {
                        Default::default()
                    }
                }),
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                window_min_size: Some(size(px(760.), px(600.))),
                ..Default::default()
            },
            cx,
            |window, cx| {
                let view = cx.new(|cx| ui::ResonaApp::new(window, cx));
                let close_view = view.downgrade();
                window.on_window_should_close(cx, move |_, cx| {
                    close_view
                        .update(cx, |view, cx| view.begin_shutdown(cx))
                        .is_err()
                });
                view
            },
        )
        .expect("failed to open Resona window");
        cx.activate(true);
    });
    Ok(())
}

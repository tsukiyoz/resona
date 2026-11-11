#![cfg_attr(all(windows, not(debug_assertions)), windows_subsystem = "windows")]

mod app_icon;
mod components;
mod core;
mod model;
mod preferences;
mod presentation;
#[cfg(debug_assertions)]
mod showcase;
mod theme;
mod ui;

use anyhow::Result;
use gpui_kit::{
    AppContext, AssetSource, Bounds, KeyBinding, SharedString, WindowBounds, WindowOptions, px,
    size,
};
use std::borrow::Cow;

struct Assets;
gpui_kit::assets::icon_assets!(ChannelIcons, [Shield]);

#[cfg(test)]
mod asset_tests {
    use super::*;

    #[test]
    fn channel_permission_and_existing_icons_are_bundled() {
        for path in [
            gpui_kit::assets::IconName::Shield.path(),
            gpui_kit::assets::IconName::CircleAlert.path(),
            "resona.svg".into(),
            "resona-dark.svg".into(),
        ] {
            let bytes = Assets.load(&path).unwrap().expect("icon must be bundled");
            assert!(std::str::from_utf8(&bytes).unwrap().contains("<svg"));
            assert!(Assets.list("").unwrap().contains(&path));
        }
    }

    #[test]
    fn vector_logos_render_in_color_without_growing_the_old_png_cache() {
        let renderer = gpui_kit::SvgRenderer::new(std::sync::Arc::new(Assets));
        for (bytes, background) in [
            (crate::app_icon::LIGHT, [255, 255, 255, 255]),
            (crate::app_icon::DARK, [21, 19, 17, 255]),
        ] {
            let image = gpui_kit::Image::from_bytes(gpui_kit::ImageFormat::Svg, bytes.to_vec())
                .to_image_data(renderer.clone())
                .expect("logo must render through the actual GPUI image path");
            assert_eq!(image.size(0).width.0, 256);
            assert_eq!(image.size(0).height.0, 256);
            let pixels = image.as_bytes(0).unwrap();
            let offset = (128 * 256 + 128) * 4;
            assert_eq!(&pixels[offset..offset + 4], &background);
            assert_eq!(pixels[3], 0, "rounded corners stay transparent");
        }
    }
}

impl AssetSource for Assets {
    fn load(&self, path: &str) -> Result<Option<Cow<'static, [u8]>>> {
        let asset: Option<&'static [u8]> = match path {
            "resona.svg" => Some(include_bytes!("../assets/resona.svg")),
            "resona-dark.svg" => Some(include_bytes!("../assets/resona-dark.svg")),
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
        if let Some(asset) = ChannelIcons.load(path)? {
            return Ok(Some(asset));
        }
        gpui_kit::assets::Assets.load(path)
    }

    fn list(&self, path: &str) -> Result<Vec<SharedString>> {
        let mut assets = gpui_kit::assets::Assets.list(path)?;
        assets.extend(ChannelIcons.list(path)?);
        for asset in [
            "resona.svg",
            "resona-dark.svg",
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
    resona_client::updates::bootstrap();
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
            KeyBinding::new(
                "escape",
                ui::DismissModal,
                Some("!PopupMenu && !PopoverContent"),
            ),
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
        #[cfg(debug_assertions)]
        if std::env::args().nth(1).as_deref() == Some("--showcase") {
            showcase::open(cx);
            cx.activate(true);
            return;
        }
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
                window.on_window_should_close(cx, move |window, cx| {
                    close_view
                        .update(cx, |view, cx| view.begin_shutdown(window, cx))
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

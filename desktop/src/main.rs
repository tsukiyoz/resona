#![cfg_attr(all(windows, not(debug_assertions)), windows_subsystem = "windows")]

mod core;
mod model;
mod preferences;
mod ui;

use anyhow::Result;
use gpui::{
    AppContext, Application, AssetSource, Bounds, KeyBinding, SharedString, WindowBounds,
    WindowOptions, px, size,
};
use gpui_component::{Root, Theme, ThemeMode, input::Enter};
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
            _ => None,
        };
        if let Some(asset) = asset {
            return Ok(Some(Cow::Borrowed(asset)));
        }
        gpui_component_assets::Assets.load(path)
    }

    fn list(&self, path: &str) -> Result<Vec<SharedString>> {
        let mut assets = gpui_component_assets::Assets.list(path)?;
        for asset in [
            "resona.svg",
            "mic.svg",
            "mic-off.svg",
            "headphones.svg",
            "headphones-off.svg",
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
    Application::new().with_assets(Assets).run(|cx| {
        gpui_component::init(cx);
        cx.bind_keys([
            KeyBinding::new("escape", ui::DismissModal, None),
            KeyBinding::new("cmd-q", ui::Quit, None),
            KeyBinding::new("ctrl-q", ui::Quit, None),
            KeyBinding::new("shift-enter", Enter { secondary: true }, Some("Input")),
        ]);
        let bounds = Bounds::centered(None, size(px(1240.), px(780.)), cx);
        cx.on_window_closed(|cx| {
            if cx.windows().is_empty() {
                cx.quit();
            }
        })
        .detach();
        cx.open_window(
            WindowOptions {
                titlebar: Some(gpui::TitlebarOptions {
                    title: Some(concat!("Resona v", env!("CARGO_PKG_VERSION")).into()),
                    ..Default::default()
                }),
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                window_min_size: Some(size(px(900.), px(600.))),
                ..Default::default()
            },
            |window, cx| {
                Theme::change(ThemeMode::Dark, Some(window), cx);
                let view = cx.new(|cx| ui::ResonaApp::new(window, cx));
                let close_view = view.downgrade();
                window.on_window_should_close(cx, move |_, cx| {
                    close_view
                        .update(cx, |view, cx| view.begin_shutdown(cx))
                        .is_err()
                });
                cx.new(|cx| Root::new(view, window, cx))
            },
        )
        .expect("failed to open Resona window");
        cx.activate(true);
    });
    Ok(())
}

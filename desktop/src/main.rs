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
        let bounds = Bounds::centered(None, size(px(800.), px(900.)), cx);
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
                    ..if cfg!(target_os = "macos") {
                        gpui_component::TitleBar::title_bar_options()
                    } else {
                        Default::default()
                    }
                }),
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                window_min_size: Some(size(px(760.), px(600.))),
                ..Default::default()
            },
            |window, cx| {
                Theme::change(ThemeMode::Dark, Some(window), cx);
                let theme = Theme::global_mut(cx);
                theme.font_size = px(14.);
                theme.radius = px(6.);
                theme.radius_lg = px(8.);
                theme.background = gpui::rgb(0x101113).into();
                theme.foreground = gpui::rgb(0xe8ecef).into();
                theme.border = gpui::rgb(0x35393e).into();
                theme.input = gpui::rgb(0x3b4046).into();
                theme.primary = gpui::rgb(0xdce6ec).into();
                theme.primary_foreground = gpui::rgb(0x14191d).into();
                theme.primary_hover = gpui::rgb(0xf0f5f7).into();
                theme.primary_active = gpui::rgb(0xb8c7d1).into();
                theme.secondary = gpui::rgb(0x202428).into();
                theme.secondary_hover = gpui::rgb(0x2d3237).into();
                theme.secondary_active = gpui::rgb(0x353c42).into();
                theme.secondary_foreground = gpui::rgb(0xd2dae0).into();
                theme.accent = gpui::rgb(0x293632).into();
                theme.accent_foreground = gpui::rgb(0xb0d6c8).into();
                theme.muted = gpui::rgb(0x22262a).into();
                theme.muted_foreground = gpui::rgb(0x929ca7).into();
                theme.ring = gpui::rgb(0x8bada0).into();
                theme.popover = gpui::rgb(0x191c20).into();
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

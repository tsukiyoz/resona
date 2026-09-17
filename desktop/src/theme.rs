use crate::preferences::ThemePreference;
use gpui::{App, Rgba, Window, rgb as gpui_rgb, rgba as gpui_rgba};
use gpui_component::{Theme, ThemeMode};
use std::sync::atomic::{AtomicBool, Ordering};

// The desktop currently has one window; color tokens are resolved while it renders.
static LIGHT: AtomicBool = AtomicBool::new(false);

pub fn is_light() -> bool {
    LIGHT.load(Ordering::Relaxed)
}

pub fn apply(preference: ThemePreference, window: Option<&mut Window>, cx: &mut App) {
    let mode = match preference {
        ThemePreference::Dark => ThemeMode::Dark,
        ThemePreference::Light => ThemeMode::Light,
        ThemePreference::System => window.as_ref().map_or_else(
            || cx.window_appearance().into(),
            |window| window.appearance().into(),
        ),
    };
    LIGHT.store(mode == ThemeMode::Light, Ordering::Relaxed);
    Theme::change(mode, window, cx);
    let theme = Theme::global_mut(cx);
    theme.font_size = gpui::px(14.);
    theme.radius = gpui::px(6.);
    theme.radius_lg = gpui::px(8.);
    let tone = |dark, light| {
        gpui_rgb(if mode == ThemeMode::Light {
            light
        } else {
            dark
        })
        .into()
    };
    theme.background = tone(0x101113, 0xffffff);
    theme.foreground = tone(0xe8ecef, 0x202428);
    theme.border = tone(0x35393e, 0xd3d8de);
    theme.input = tone(0x3b4046, 0xe7eaed);
    theme.primary = tone(0xdce6ec, 0x1d292f);
    theme.primary_foreground = tone(0x14191d, 0xffffff);
    theme.primary_hover = tone(0xf0f5f7, 0x17252c);
    theme.primary_active = tone(0xb8c7d1, 0x34444d);
    theme.secondary = tone(0x202428, 0xf0f2f4);
    theme.secondary_hover = tone(0x2d3237, 0xe6eaee);
    theme.secondary_active = tone(0x353c42, 0xdce2e7);
    theme.secondary_foreground = tone(0xd2dae0, 0x303d44);
    theme.accent = tone(0x293632, 0xeaf3f0);
    theme.accent_foreground = tone(0xb0d6c8, 0x28594c);
    theme.muted = tone(0x22262a, 0xf0f2f3);
    theme.muted_foreground = tone(0x929ca7, 0x66727d);
    theme.ring = tone(0x8bada0, 0x43816c);
    theme.popover = tone(0x191c20, 0xffffff);
}

pub fn rgb(dark: u32) -> Rgba {
    if !is_light() {
        return gpui_rgb(dark);
    }
    let light = match dark {
        0x090a0c => 0xf6f8fa,
        0x101214 | 0x111315 => 0xffffff,
        0x181a1d => 0xf0f3f5,
        0x24272b | 0x2a2f31 => 0xe8eef1,
        0x303337 | 0x303638 | 0x303840 => 0xd8dfe4,
        0xe9edf1 => 0x1e2930,
        0x929ca7 => 0x64727d,
        0x7db8e8 => 0x216894,
        0x79c9ad => 0x276d56,
        0xd8aa5d => 0x8d6022,
        0xe28282 => 0xa63840,
        0x40282b | 0x3b2528 => 0xffecee,
        0x724146 => 0xe5aeb4,
        0x293735 => 0xe5f1ed,
        _ => dark,
    };
    gpui_rgb(light)
}

pub fn rgba(dark: u32) -> Rgba {
    if !is_light() || dark == 0x00000066 || dark == 0x00000000 {
        return gpui_rgba(dark);
    }
    let light = match dark {
        0xffffff00 => 0x00000000,
        0xffffff06 => 0x00000006,
        0xffffff14 => 0x0000000d,
        0xffffff20 => 0x00000010,
        0xffffff22 | 0xffffff24 => 0x00000016,
        0xffffff38 | 0xffffff40 => 0x00000028,
        0x9dbdafaa => 0x36765caa,
        0x16181bed | 0x141619f5 | 0x17191df5 => 0xffffffff,
        0x101214ed => 0xfffffff0,
        0x080a0d55 => 0xe8edf080,
        0x08090bdd => 0xffffffdd,
        _ => dark,
    };
    gpui_rgba(light)
}

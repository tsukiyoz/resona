use crate::preferences::ThemePreference;
use gpui_kit::component::{Theme, ThemeMode};
use gpui_kit::{App, Rgba, Window, rgb as gpui_rgb, rgba as gpui_rgba};
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
    crate::app_icon::apply(mode == ThemeMode::Light, window.as_deref());
    #[cfg(target_os = "windows")]
    if let Some(window) = window.as_deref() {
        apply_titlebar(mode, window);
    }
    Theme::change(mode, window, cx);
    Theme::update(cx, |theme| {
        theme.font_size = gpui_kit::px(14.);
        theme.radius = gpui_kit::px(6.);
        theme.radius_lg = gpui_kit::px(8.);
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
    });
}

#[cfg(target_os = "windows")]
fn apply_titlebar(mode: ThemeMode, window: &Window) {
    use raw_window_handle::{HasWindowHandle, RawWindowHandle};
    use windows::{
        Win32::{
            Foundation::HWND,
            Graphics::Dwm::{DWMWA_USE_IMMERSIVE_DARK_MODE, DwmSetWindowAttribute},
        },
        core::BOOL,
    };

    let Ok(handle) = HasWindowHandle::window_handle(window) else {
        return;
    };
    let RawWindowHandle::Win32(handle) = handle.as_raw() else {
        return;
    };
    let dark: BOOL = (mode == ThemeMode::Dark).into();
    // The live HWND belongs to this UI thread; older Windows may ignore this attribute.
    unsafe {
        let _ = DwmSetWindowAttribute(
            HWND(handle.hwnd.get() as *mut _),
            DWMWA_USE_IMMERSIVE_DARK_MODE,
            std::ptr::from_ref(&dark).cast(),
            std::mem::size_of_val(&dark) as u32,
        );
    }
}

// Exhaustive semantic roles: adding a role requires an explicit pair of palettes.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Color {
    Canvas,
    Panel,
    Raised,
    Hover,
    Border,
    Text,
    SecondaryText,
    Muted,
    Speaking,
    Idle,
    Success,
    Warning,
    Danger,
    DangerSurface,
    DangerBorder,
    Avatar,
    AvatarAccent,
    Sidebar,
    Selected,
    Meter,
    Transparent,
    SelectionBorder,
    SubtleOverlay,
    HighlightOverlay,
    HoverOverlay,
    ControlBorder,
    GlassBorder,
    ModalBorder,
    Popover,
    Toolbar,
    Modal,
    SidebarShade,
    Toast,
    Scrim,
}

impl Color {
    fn palette(self, light: bool) -> u32 {
        let (dark, light_color) = match self {
            Self::Canvas => (0x090a0cff, 0xf6f8faff),
            Self::Panel => (0x101214ff, 0xffffffff),
            Self::Raised => (0x181a1dff, 0xf0f3f5ff),
            Self::Hover => (0x24272bff, 0xe8eef1ff),
            Self::Border => (0x303337ff, 0xd8dfe4ff),
            Self::Text => (0xe9edf1ff, 0x1e2930ff),
            Self::SecondaryText => (0xc3c9cfff, 0x46535eff),
            Self::Muted => (0x929ca7ff, 0x5d6a75ff),
            Self::Speaking => (0x7db8e8ff, 0x216894ff),
            Self::Idle => (0x66717cff, 0x74818cff),
            Self::Success => (0x79c9adff, 0x276d56ff),
            Self::Warning => (0xd8aa5dff, 0x8d6022ff),
            Self::Danger => (0xe28282ff, 0xa63840ff),
            Self::DangerSurface => (0x40282bff, 0xffeceeff),
            Self::DangerBorder => (0x724146ff, 0xe5aeb4ff),
            Self::Avatar => (0x303638ff, 0xd8dfe4ff),
            Self::AvatarAccent => (0x293735ff, 0xe5f1edff),
            Self::Sidebar => (0x111315ff, 0xffffffff),
            Self::Selected => (0x2a2f31ff, 0xe8eef1ff),
            Self::Meter => (0x303840ff, 0xd8dfe4ff),
            Self::Transparent => (0x00000000, 0x00000000),
            Self::SelectionBorder => (0x9dbdafaa, 0x36765caa),
            Self::SubtleOverlay => (0xffffff06, 0x00000006),
            Self::HighlightOverlay => (0xffffff14, 0x0000000d),
            Self::HoverOverlay => (0xffffff20, 0x00000010),
            Self::ControlBorder => (0xffffff40, 0x00000028),
            Self::GlassBorder => (0xffffff22, 0x00000016),
            Self::ModalBorder => (0xffffff38, 0x00000028),
            Self::Popover => (0x16181bed, 0xffffffff),
            Self::Toolbar => (0x141619f5, 0xffffffff),
            Self::Modal => (0x17191df5, 0xffffffff),
            Self::SidebarShade => (0x080a0d55, 0xe8edf080),
            Self::Toast => (0x101214ed, 0xfffffff0),
            Self::Scrim => (0x00000066, 0x00000066),
        };
        if light { light_color } else { dark }
    }
}

pub fn color(role: Color) -> Rgba {
    gpui_rgba(role.palette(is_light()))
}

#[cfg(test)]
mod tests {
    use super::Color;

    fn contrast(foreground: u32, background: u32) -> f64 {
        let luminance = |color: u32| {
            let component = |shift: u32| {
                let c = f64::from((color >> shift) & 255_u32) / 255.;
                if c <= 0.04045 {
                    c / 12.92
                } else {
                    ((c + 0.055) / 1.055).powf(2.4)
                }
            };
            0.2126 * component(24) + 0.7152 * component(16) + 0.0722 * component(8)
        };
        let (a, b) = (luminance(foreground), luminance(background));
        (a.max(b) + 0.05) / (a.min(b) + 0.05)
    }

    #[test]
    fn text_and_status_remain_readable_in_both_palettes() {
        for light in [false, true] {
            for role in [
                Color::Text,
                Color::SecondaryText,
                Color::Muted,
                Color::Danger,
                Color::Warning,
                Color::Success,
                Color::Speaking,
            ] {
                for surface in [Color::Canvas, Color::Panel, Color::Raised] {
                    assert!(
                        contrast(role.palette(light), surface.palette(light)) >= 4.5,
                        "{role:?} on {surface:?}, light={light}"
                    );
                }
            }
        }
    }
}

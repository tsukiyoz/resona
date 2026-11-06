//! Shared production rendering used by the application and the debug showcase.
use crate::{
    model::{User, VoiceState},
    presentation::ServerEntry,
    theme::{Color, color},
};
use gpui_kit::component::tooltip::Tooltip;
use gpui_kit::component::{
    Icon, IconName, Sizable,
    button::{Button, ButtonVariants},
    scroll::ScrollableElement,
};
use gpui_kit::{
    AnyElement, InteractiveElement, IntoElement, ParentElement, SharedString,
    StatefulInteractiveElement, Styled, div, prelude::*, px, svg,
};

pub fn error_notice(
    text: String,
    dismiss: impl Fn(&gpui_kit::ClickEvent, &mut gpui_kit::Window, &mut gpui_kit::App) + 'static,
) -> impl IntoElement {
    div()
        .id("error-notice")
        .max_w(px(360.))
        .min_w_0()
        .px_3()
        .py_2()
        .rounded(px(8.))
        .bg(color(Color::Panel))
        .border_1()
        .border_color(color(Color::Border))
        .shadow_sm()
        .flex()
        .items_start()
        .gap_2()
        .child(
            div().h(px(24.)).flex_none().flex().items_center().child(
                Icon::new(gpui_kit::assets::IconName::CircleAlert)
                    .size(px(16.))
                    .text_color(color(Color::Danger)),
            ),
        )
        .child(
            div()
                .id("error-notice-text")
                .min_w_0()
                .max_h(px(144.))
                .py(px(2.))
                .pr_2()
                .text_sm()
                .line_height(px(20.))
                .text_color(color(Color::Text))
                .child(text)
                .overflow_y_scrollbar(),
        )
        .child(
            Button::new("dismiss-error")
                .icon(IconName::Close)
                .ghost()
                .xsmall()
                .w(px(24.))
                .h(px(24.))
                .flex_none()
                .tooltip("关闭通知")
                .on_click(dismiss),
        )
}

pub fn attention_notice(
    text: String,
    pending: usize,
    dismiss: impl Fn(&gpui_kit::ClickEvent, &mut gpui_kit::Window, &mut gpui_kit::App) + 'static,
) -> impl IntoElement {
    div()
        .id("poke-notice")
        .w(px(320.))
        .max_w_full()
        .p_3()
        .rounded(px(6.))
        .bg(color(Color::Panel))
        .border_1()
        .border_color(color(Color::SelectionBorder))
        .flex()
        .items_center()
        .gap_2()
        .child(
            div()
                .flex_1()
                .min_w_0()
                .text_sm()
                .child(text)
                .when(pending > 0, |text| {
                    text.child(
                        div()
                            .text_xs()
                            .text_color(color(Color::Muted))
                            .child(format!("另有 {pending} 条提醒")),
                    )
                }),
        )
        .child(
            Button::new("dismiss-poke")
                .icon(IconName::Close)
                .ghost()
                .tooltip("关闭提醒")
                .on_click(dismiss),
        )
}

pub fn server_entry_contents(entry: &ServerEntry<'_>) -> gpui_kit::Div {
    div()
        .w_full()
        .max_w(px(380.))
        .flex()
        .flex_col()
        .items_center()
        .gap_3()
        .child(
            div()
                .h(px(20.))
                .text_sm()
                .text_color(color(if entry.failed {
                    Color::Danger
                } else {
                    Color::Muted
                }))
                .child(if entry.waiting { "" } else { entry.title }),
        )
        .when(!entry.detail.is_empty(), |body| {
            body.child(
                div()
                    .w_full()
                    .text_xs()
                    .text_color(color(Color::Muted))
                    .child(entry.detail.to_owned()),
            )
        })
}

pub fn speaking_indicator(
    user: &User,
    voice: &VoiceState,
    speaking: bool,
    location: &'static str,
) -> AnyElement {
    let locally_muted = !user.is_self && user.playback_muted;
    let input_muted = if user.is_self {
        voice.muted
    } else {
        user.voice_state_known && user.input_muted
    };
    let output_muted = if user.is_self {
        voice.deafened
    } else {
        user.voice_state_known && user.output_muted
    };
    let (icon, tooltip) = if locally_muted {
        (Some("headphones-off.svg"), "已在本机静音")
    } else if input_muted {
        (Some("mic-off.svg"), "麦克风已静音")
    } else if speaking && user.is_self {
        (None, "正在发送语音")
    } else if speaking {
        (None, "正在播放语音")
    } else if output_muted {
        (Some("headphones-off.svg"), "收听已关闭")
    } else {
        (None, "未在说话")
    };
    let tooltip: SharedString = tooltip.into();
    div()
        .id(SharedString::from(format!(
            "speaking-{location}-{}",
            user.id
        )))
        .size(px(16.))
        .flex_none()
        .flex()
        .items_center()
        .justify_center()
        .tooltip(move |window, cx| Tooltip::new(tooltip.clone()).build(window, cx))
        .child(if let Some(path) = icon {
            svg()
                .path(path)
                .text_color(color(if locally_muted {
                    Color::Warning
                } else {
                    Color::Muted
                }))
                .size(px(14.))
                .into_any_element()
        } else {
            div()
                .size(px(if speaking { 12. } else { 8. }))
                .rounded_full()
                .bg(color(if speaking {
                    Color::Speaking
                } else {
                    Color::Idle
                }))
                .into_any_element()
        })
        .into_any_element()
}

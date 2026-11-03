//! Debug-only local fixtures. This entry point never constructs ResonaApp or CoreClient.
use crate::{
    components::{server_entry_contents, speaking_indicator},
    model::{Capabilities, Channel, ProcessorOption, User, VoiceState, Workspace},
    preferences::ThemePreference,
    presentation::{self, ResourceState, ServerPage},
    theme::{self, Color, color},
};
use gpui_kit::component::{
    Disableable, IconName, Selectable,
    button::{Button, ButtonVariants},
    menu::{DropdownMenu, PopupMenuItem},
};
use gpui_kit::{
    App, AppContext, Bounds, Context, FocusHandle, InteractiveElement, IntoElement, ParentElement,
    Render, Styled, Window, WindowBounds, WindowOptions, div, prelude::*, px, size,
};

const SCENARIOS: [&str; 8] = [
    "离线",
    "本地预览",
    "准备连接",
    "连接中",
    "资源加载失败",
    "连接恢复失败",
    "语音失败",
    "音频不可用",
];

pub fn open(cx: &mut App) {
    let bounds = Bounds::centered(None, size(px(800.), px(900.)), cx);
    gpui_kit::open_window(
        WindowOptions {
            titlebar: Some(gpui_kit::TitlebarOptions {
                title: Some("Resona · 本地组件预览".into()),
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
            theme::apply(ThemePreference::System, Some(window), cx);
            cx.new(|cx| Showcase {
                scenario: 0,
                backend: "speex".into(),
                theme: ThemePreference::System,
                focus: {
                    let focus = cx.focus_handle();
                    focus.focus(window, cx);
                    focus
                },
            })
        },
    )
    .expect("failed to open component showcase");
}

struct Showcase {
    scenario: usize,
    backend: String,
    theme: ThemePreference,
    focus: FocusHandle,
}

impl Render for Showcase {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let (workspace, resources, preparing, capabilities) = fixture(self.scenario);
        let selected = if self.scenario == 1 {
            "__preview__"
        } else {
            "fixture"
        };
        let page = presentation::server_page(&workspace, selected, resources, preparing, false);
        let status = presentation::session_status(&workspace);
        let entry = presentation::server_entry(
            &workspace,
            selected,
            page,
            "服务器信息加载失败，请重试。",
            true,
        );
        let processor =
            presentation::processor_menu(&capabilities, "preprocess", "ans", &self.backend);
        let mut scenarios = div().flex().flex_wrap().gap_2();
        for (index, label) in SCENARIOS.into_iter().enumerate() {
            scenarios = scenarios.child(
                Button::new(("scenario", index))
                    .label(label)
                    .ghost()
                    .selected(self.scenario == index)
                    .on_click(cx.listener(move |this, _, _, cx| {
                        this.scenario = index;
                        cx.notify();
                    })),
            );
        }
        let mut themes = div().flex().gap_2();
        for (index, (label, preference)) in [
            ("系统", ThemePreference::System),
            ("浅色", ThemePreference::Light),
            ("深色", ThemePreference::Dark),
        ]
        .into_iter()
        .enumerate()
        {
            themes = themes.child(
                Button::new(("preview-theme", index))
                    .label(label)
                    .ghost()
                    .selected(self.theme == preference)
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.theme = preference;
                        theme::apply(preference, Some(window), cx);
                        cx.notify();
                    })),
            );
        }
        let view = cx.entity();
        let mut members = div().flex().flex_col().gap_2();
        for (index, (label, speaking, muted)) in [
            ("空闲成员", false, false),
            ("正在发言", true, false),
            ("已静音成员", false, true),
        ]
        .into_iter()
        .enumerate()
        {
            let user = User {
                id: index.to_string(),
                nickname: label.into(),
                voice_state_known: true,
                input_muted: muted,
                ..Default::default()
            };
            members = members.child(
                div()
                    .h(px(32.))
                    .flex()
                    .items_center()
                    .gap_2()
                    .child(speaking_indicator(
                        &user,
                        &VoiceState::default(),
                        speaking,
                        "showcase",
                    ))
                    .child(div().text_sm().child(label)),
            );
        }
        div()
            .track_focus(&self.focus)
            .on_action(cx.listener(|_, _: &crate::ui::Quit, _, cx| cx.quit()))
            .h(window.viewport_size().height)
            .flex()
            .flex_col()
            .bg(color(Color::Panel))
            .text_color(color(Color::Text))
            .when(cfg!(target_os = "macos"), |root| {
                root.child(
                    gpui_kit::component::TitleBar::new()
                        .bg(color(Color::Panel))
                        .border_b_0(),
                )
            })
            .child(
                div()
                    .p_4()
                    .flex()
                    .items_center()
                    .justify_between()
                    .child(
                        div().flex().flex_col().gap_1().child("本地组件预览").child(
                            div()
                                .text_xs()
                                .text_color(color(Color::Muted))
                                .child("调试数据 · 未连接网络或音频设备"),
                        ),
                    )
                    .child(themes),
            )
            .child(div().px_4().pb_3().child(scenarios))
            .child(
                div()
                    .flex_1()
                    .min_h_0()
                    .flex()
                    .border_t_1()
                    .border_color(color(Color::Border))
                    .child(
                        div()
                            .w(px(210.))
                            .flex_shrink_0()
                            .p_4()
                            .bg(color(Color::Sidebar))
                            .border_r_1()
                            .border_color(color(Color::Border))
                            .flex()
                            .flex_col()
                            .gap_4()
                            .child("成员状态")
                            .child(members),
                    )
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .p_5()
                            .flex()
                            .flex_col()
                            .gap_5()
                            .bg(color(Color::Canvas))
                            .child(
                                div()
                                    .text_sm()
                                    .text_color(color(status.tone))
                                    .child(status.label),
                            )
                            .child(
                                div()
                                    .flex_1()
                                    .min_h_0()
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .child(if page == ServerPage::Channels {
                                        div()
                                            .flex()
                                            .flex_col()
                                            .gap_2()
                                            .text_sm()
                                            .child("频道已就绪")
                                            .when(self.scenario == 6, |body| {
                                                body.child(
                                                    div()
                                                        .text_xs()
                                                        .text_color(color(Color::Danger))
                                                        .child("音频设备不可用，请检查设备设置。"),
                                                )
                                            })
                                    } else {
                                        server_entry_contents(&entry).child(
                                            Button::new("fixture-action")
                                                .label(entry.action)
                                                .primary()
                                                .disabled(true),
                                        )
                                    }),
                            )
                            .child(
                                div()
                                    .border_t_1()
                                    .border_color(color(Color::Border))
                                    .pt_4()
                                    .flex()
                                    .flex_col()
                                    .gap_2()
                                    .child(
                                        div()
                                            .text_xs()
                                            .text_color(color(Color::Muted))
                                            .child("ANS（噪声抑制）"),
                                    )
                                    .child(
                                        Button::new("fixture-processor")
                                            .label(processor.selected_label)
                                            .tooltip(processor.selected_description)
                                            .icon(IconName::ChevronDown)
                                            .disabled(processor.options.is_empty())
                                            .dropdown_menu(move |mut menu, _, _| {
                                                for option in &processor.options {
                                                    let backend = option.id.clone();
                                                    let view = view.clone();
                                                    menu = menu.item(
                                                        PopupMenuItem::new(
                                                            option.display_name.clone(),
                                                        )
                                                        .on_click(move |_, _, cx| {
                                                            view.update(cx, |this, cx| {
                                                                this.backend = backend.clone();
                                                                cx.notify();
                                                            });
                                                        }),
                                                    );
                                                }
                                                menu
                                            }),
                                    ),
                            ),
                    ),
            )
    }
}

fn fixture(scenario: usize) -> (Workspace, ResourceState, bool, Capabilities) {
    let mut workspace = Workspace::default();
    workspace.session.server_id = "fixture".into();
    workspace.session.server_name = "本地测试服务器".into();
    workspace.session.mode = match scenario {
        1 => "preview",
        3 => "connecting",
        4 | 6 | 7 => "connected",
        5 => "reconnecting",
        _ => "offline",
    }
    .into();
    workspace.session.connection_retry_slow = scenario == 5;
    if scenario == 1 {
        workspace.channels.push(Channel {
            id: "channel".into(),
            name: "预览频道".into(),
            ..Default::default()
        });
    }
    let capabilities = Capabilities {
        protocol_version: 1,
        voice: scenario != 7,
        audio_processors: [
            ("none", "关闭", "不进行此项处理。"),
            ("speex", "SpeexDSP", "轻量降噪，适合稳定噪声。"),
        ]
        .into_iter()
        .map(|(id, label, description)| ProcessorOption {
            phase: "preprocess".into(),
            name: "ans".into(),
            id: id.into(),
            display_name: label.into(),
            description: description.into(),
        })
        .collect(),
        ..Default::default()
    };
    (
        workspace,
        if scenario == 4 {
            ResourceState::Failed
        } else {
            ResourceState::Ready
        },
        scenario == 2,
        capabilities,
    )
}

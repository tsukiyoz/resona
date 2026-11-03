//! Pure presentation derived from Core facts; no requests, timers or retained UI state.
use crate::{
    model::{Capabilities, ProcessorOption, Workspace},
    theme::Color,
};

pub struct ProcessorMenu {
    pub options: Vec<ProcessorOption>,
    pub description: String,
    pub selected_label: String,
    pub selected_description: String,
}

pub fn processor_menu(
    capabilities: &Capabilities,
    phase: &str,
    name: &str,
    backend: &str,
) -> ProcessorMenu {
    let options = capabilities
        .audio_processors
        .iter()
        .filter(|option| capabilities.voice && option.phase == phase && option.name == name)
        .cloned()
        .collect::<Vec<_>>();
    let mut description = options
        .iter()
        .filter(|option| option.id != "none")
        .map(|option| format!("{}：{}", option.display_name, option.description))
        .collect::<Vec<_>>()
        .join("\n");
    if description.is_empty() {
        description = "音频处理器信息尚未就绪；若持续不可用，请重启应用。".into();
    }
    let selected = options.iter().find(|option| option.id == backend);
    let selected_label = selected
        .map(|option| option.display_name.clone())
        .unwrap_or_else(|| {
            format!(
                "{}（{}）",
                backend,
                if options.is_empty() {
                    "信息未获取"
                } else {
                    "当前不可用"
                }
            )
        });
    let selected_description = selected
        .map(|option| option.description.clone())
        .unwrap_or_else(|| "此处理器当前不可用，请检查安装是否完整并重启应用。".into());
    ProcessorMenu {
        options,
        description,
        selected_label,
        selected_description,
    }
}

pub struct SessionStatus {
    pub label: &'static str,
    pub tone: Color,
    pub detail: String,
}

pub fn session_status(workspace: &Workspace) -> SessionStatus {
    let session = &workspace.session;
    let (label, tone) = match session.mode.as_str() {
        "preview" => ("本地预览", Color::Warning),
        "connecting" | "reconnecting" => ("未连接", Color::Warning),
        "connected" => ("在线", Color::Success),
        "disconnecting" => ("正在断开", Color::Warning),
        "failed" => ("离线", Color::Danger),
        _ => ("离线", Color::Muted),
    };
    let detail = if !session.error.is_empty() {
        session.error.clone()
    } else if !session.credential_error.is_empty() {
        session.credential_error.clone()
    } else if session.member_sync_state == "limited" {
        format!("成员范围受限 · {}", session.member_sync_error)
    } else if session.member_sync_state == "pending" && workspace.connected() {
        "正在同步可见成员".into()
    } else {
        session.server_name.clone()
    };
    SessionStatus {
        label,
        tone,
        detail,
    }
}

pub struct ServerEntry<'a> {
    pub title: &'static str,
    pub detail: &'a str,
    pub action: &'static str,
    pub failed: bool,
    pub waiting: bool,
}

pub fn server_entry<'a>(
    workspace: &'a Workspace,
    selected: &str,
    page: ServerPage,
    resource_error: &'a str,
    has_profile: bool,
) -> ServerEntry<'a> {
    let title = match page {
        ServerPage::ConnectionFailed => "连接失败",
        ServerPage::ResourceFailed => "无法加载服务器信息",
        _ if selected == "__preview__" && workspace.connected() => "断开当前连接后可进入本地预览",
        _ if !has_profile => "选择或添加服务器",
        _ => "此服务器尚未连接",
    };
    let detail = match page {
        ServerPage::ConnectionFailed if workspace.session.mode == "reconnecting" => {
            "连接已中断，正在后台尝试恢复。你可以重新连接或取消。"
        }
        ServerPage::ConnectionFailed if workspace.session.error.is_empty() => {
            "无法连接服务器，请检查网络和服务器设置后重试。"
        }
        ServerPage::ConnectionFailed => workspace.session.error.as_str(),
        ServerPage::ResourceFailed => resource_error,
        _ => "",
    };
    let action = match page {
        ServerPage::Connecting => "正在连接…",
        ServerPage::Disconnecting => "正在取消…",
        ServerPage::ConnectionFailed => "重试连接",
        ServerPage::ResourceFailed => "重试加载",
        _ if workspace.connected() => "切换连接",
        _ => "连接服务器",
    };
    ServerEntry {
        title,
        detail,
        action,
        failed: matches!(
            page,
            ServerPage::ConnectionFailed | ServerPage::ResourceFailed
        ),
        waiting: matches!(page, ServerPage::Connecting | ServerPage::Disconnecting),
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum ResourceState {
    #[default]
    Idle,
    Syncing,
    Ready,
    Refreshing,
    Failed,
}

impl ResourceState {
    pub fn blocks(self) -> bool {
        matches!(self, Self::Idle | Self::Syncing | Self::Failed)
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ServerPage {
    Channels,
    Offline,
    Connecting,
    Disconnecting,
    ConnectionFailed,
    ResourceFailed,
}

pub fn can_show_session_channels(workspace: &Workspace, selected: &str) -> bool {
    if workspace.channels.is_empty() {
        return false;
    }
    if workspace.session.mode == "preview" {
        return selected == "__preview__" || !workspace.servers.iter().any(|s| s.id == selected);
    }
    workspace.connected() && selected == workspace.session.server_id
}

pub fn server_page(
    workspace: &Workspace,
    selected: &str,
    resources: ResourceState,
    preparing: bool,
    disconnecting: bool,
) -> ServerPage {
    if preparing {
        return if disconnecting {
            ServerPage::Disconnecting
        } else {
            ServerPage::Connecting
        };
    }
    if workspace.session.mode == "preview" && can_show_session_channels(workspace, selected) {
        return ServerPage::Channels;
    }
    if selected != workspace.session.server_id {
        return ServerPage::Offline;
    }
    if disconnecting || workspace.session.mode == "disconnecting" {
        return ServerPage::Disconnecting;
    }
    match workspace.session.mode.as_str() {
        "reconnecting" if workspace.session.connection_retry_slow => ServerPage::ConnectionFailed,
        "connecting" | "reconnecting" => ServerPage::Connecting,
        "connected" => match resources {
            ResourceState::Failed => ServerPage::ResourceFailed,
            ResourceState::Idle | ResourceState::Syncing => ServerPage::Connecting,
            ResourceState::Ready | ResourceState::Refreshing => ServerPage::Channels,
        },
        "failed" => ServerPage::ConnectionFailed,
        _ => ServerPage::Offline,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn resource_failure_retries_loading_without_claiming_disconnection() {
        let mut workspace = Workspace::default();
        workspace.session.mode = "connected".into();
        workspace.session.server_id = "server".into();
        let page = server_page(&workspace, "server", ResourceState::Failed, false, false);
        let entry = server_entry(&workspace, "server", page, "fetch failed", true);
        assert_eq!(entry.action, "重试加载");
        assert_eq!(entry.detail, "fetch failed");
        assert!(entry.failed && !entry.waiting);
        assert_eq!(session_status(&workspace).label, "在线");
        assert_eq!(
            server_page(&workspace, "other", ResourceState::Failed, false, false),
            ServerPage::Offline
        );
    }

    #[test]
    fn unavailable_capabilities_preserve_the_selection_without_offering_it() {
        let mut capabilities = Capabilities {
            voice: true,
            ..Default::default()
        };
        capabilities.audio_processors.push(ProcessorOption {
            phase: "preprocess".into(),
            name: "ans".into(),
            id: "speex".into(),
            display_name: "SpeexDSP".into(),
            description: "轻量降噪".into(),
        });
        let current = processor_menu(&capabilities, "preprocess", "ans", "speex");
        assert_eq!(current.selected_label, "SpeexDSP");
        let missing = processor_menu(&capabilities, "preprocess", "ans", "rnnoise");
        assert!(missing.selected_label.contains("当前不可用"));
        assert!(missing.options.iter().all(|option| option.id != "rnnoise"));
        assert!(
            processor_menu(&capabilities, "postprocess", "agc", "speex")
                .options
                .is_empty()
        );
        capabilities.voice = false;
        assert!(
            processor_menu(&capabilities, "preprocess", "ans", "speex")
                .options
                .is_empty()
        );
    }
}

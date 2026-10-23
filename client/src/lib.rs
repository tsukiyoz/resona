//! UI-independent Rust client core shared by the GPUI desktop and local tools.
#![deny(unsafe_code)]

#[cfg(feature = "audio")]
pub mod audio;

pub mod build_info;
pub mod config;
#[cfg(feature = "audio")]
pub mod core;
pub mod credentials;
pub mod diagnostics;
pub mod lifecycle;
pub mod model;
pub mod noise;
pub mod resources;
pub mod session;
pub mod updates;
pub mod wire;
pub mod workspace;

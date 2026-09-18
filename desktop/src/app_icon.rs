pub const LIGHT: &[u8] = include_bytes!("../assets/resona-light.png");
pub const DARK: &[u8] = include_bytes!("../assets/resona-dark.png");

pub fn apply(light: bool, window: Option<&gpui::Window>) {
    platform::apply(light, window);
}

#[cfg(target_os = "macos")]
mod platform {
    use objc2::{MainThreadMarker, rc::Retained};
    use objc2_app_kit::{NSApplication, NSImage};
    use objc2_foundation::NSData;
    use std::cell::RefCell;

    #[derive(Default)]
    struct Icons {
        active: Option<bool>,
        images: [Option<Retained<NSImage>>; 2],
    }

    thread_local! {
        static ICONS: RefCell<Icons> = RefCell::new(Icons::default());
    }

    pub fn apply(light: bool, _: Option<&gpui::Window>) {
        let Some(main_thread) = MainThreadMarker::new() else {
            return;
        };
        ICONS.with(|cache| {
            let mut cache = cache.borrow_mut();
            if cache.active == Some(light) {
                return;
            }
            let image = cache.images[usize::from(light)].get_or_insert_with(|| {
                NSImage::initWithData(
                    main_thread.alloc(),
                    &NSData::with_bytes(if light {
                        include_bytes!("../assets/resona-macos-light.png")
                    } else {
                        include_bytes!("../assets/resona-macos-dark.png")
                    }),
                )
                .expect("embedded application icon must be a valid PNG")
            });
            // AppKit is main-thread-only; the retained image is always non-null.
            unsafe {
                NSApplication::sharedApplication(main_thread).setApplicationIconImage(Some(image));
            }
            cache.active = Some(light);
        });
    }
}

#[cfg(target_os = "windows")]
mod platform {
    use raw_window_handle::{HasWindowHandle, RawWindowHandle};
    use std::cell::RefCell;
    use windows::{
        Win32::{
            Foundation::{HINSTANCE, HWND, LPARAM, WPARAM},
            System::LibraryLoader::GetModuleHandleW,
            UI::WindowsAndMessaging::{
                DestroyIcon, GetSystemMetrics, HICON, ICON_BIG, ICON_SMALL, IMAGE_FLAGS,
                IMAGE_ICON, LoadImageW, SM_CXICON, SM_CXSMICON, SM_CYICON, SM_CYSMICON,
                SendMessageW, WM_SETICON,
            },
        },
        core::PCWSTR,
    };

    struct OwnedIcon(HICON);

    impl Drop for OwnedIcon {
        fn drop(&mut self) {
            // These handles were loaded without LR_SHARED and remain cached
            // until UI-thread teardown, after the application window closes.
            unsafe {
                let _ = DestroyIcon(self.0);
            }
        }
    }

    #[derive(Default)]
    struct Icons {
        active: Option<(isize, bool)>,
        images: [Option<(OwnedIcon, OwnedIcon)>; 2],
    }

    thread_local! {
        static ICONS: RefCell<Icons> = RefCell::new(Icons::default());
    }

    pub fn apply(light: bool, window: Option<&gpui::Window>) {
        let Some(window) = window else { return };
        let Ok(handle) = HasWindowHandle::window_handle(window) else {
            return;
        };
        let RawWindowHandle::Win32(handle) = handle.as_raw() else {
            return;
        };
        ICONS.with(|cache| {
            let mut cache = cache.borrow_mut();
            let key = (handle.hwnd.get(), light);
            if cache.active == Some(key) {
                return;
            }
            // The window is alive and owned by this UI thread. Keep both sizes
            // alive across WM_SETICON; previous class/default icons are not ours.
            unsafe {
                let index = usize::from(light);
                if cache.images[index].is_none() {
                    let Ok(module) = GetModuleHandleW(None) else {
                        return;
                    };
                    let resource = PCWSTR(if light { 1 } else { 2 } as *const u16);
                    let load = |x, y| {
                        LoadImageW(
                            Some(HINSTANCE(module.0)),
                            resource,
                            IMAGE_ICON,
                            GetSystemMetrics(x),
                            GetSystemMetrics(y),
                            IMAGE_FLAGS(0),
                        )
                        .map(|handle| OwnedIcon(HICON(handle.0)))
                    };
                    let (Ok(small), Ok(big)) =
                        (load(SM_CXSMICON, SM_CYSMICON), load(SM_CXICON, SM_CYICON))
                    else {
                        return;
                    };
                    cache.images[index] = Some((small, big));
                }
                let (small, big) = cache.images[index].as_ref().unwrap();
                let hwnd = HWND(handle.hwnd.get() as *mut _);
                SendMessageW(
                    hwnd,
                    WM_SETICON,
                    Some(WPARAM(ICON_SMALL as usize)),
                    Some(LPARAM(small.0.0 as isize)),
                );
                SendMessageW(
                    hwnd,
                    WM_SETICON,
                    Some(WPARAM(ICON_BIG as usize)),
                    Some(LPARAM(big.0.0 as isize)),
                );
            }
            cache.active = Some(key);
        });
    }
}

#[cfg(not(any(target_os = "macos", target_os = "windows")))]
mod platform {
    pub fn apply(_: bool, _: Option<&gpui::Window>) {}
}

#![cfg(target_os = "macos")]

#[test]
fn copied_block_keeps_its_capture_after_the_original_is_dropped() {
    let captured = String::from("Resona");
    let block = block::ConcreteBlock::new(move |suffix: usize| captured.len() + suffix).copy();
    let cloned = block.clone();
    drop(block);
    assert_eq!(unsafe { cloned.call((4,)) }, 10);
}

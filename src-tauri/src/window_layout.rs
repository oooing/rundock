// Reserve room for window borders, title bar and a small desktop margin.
pub fn needs_maximized(width: f64, height: f64, scale: f64, work_width: u32, work_height: u32) -> bool {
    (width + 32.0) * scale > work_width as f64
        || (height + 64.0) * scale > work_height as f64
}

#[cfg(test)]
mod tests {
    use super::needs_maximized;

    #[test]
    fn normal_desktop_has_room_for_three_columns() {
        assert!(!needs_maximized(1440.0, 900.0, 1.0, 1920, 1040));
    }

    #[test]
    fn laptop_and_high_dpi_use_available_screen() {
        assert!(needs_maximized(1440.0, 900.0, 1.0, 1366, 728));
        assert!(needs_maximized(1440.0, 900.0, 1.5, 1920, 1040));
        assert!(needs_maximized(1440.0, 900.0, 1.5, 2560, 1400));
        assert!(!needs_maximized(1440.0, 900.0, 1.5, 2880, 1700));
    }
}

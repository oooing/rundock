// Use the current Windows user's display language, not the keyboard or date format.
// https://learn.microsoft.com/windows/win32/api/winnls/nf-winnls-getuserdefaultuilanguage
#[cfg(target_os = "windows")]
pub fn detect() -> Option<&'static str> {
    #[link(name = "kernel32")]
    extern "system" {
        fn GetUserDefaultUILanguage() -> u16;
    }
    // No arguments or buffers; Windows returns a LANGID directly.
    Some(locale_for_language_id(unsafe { GetUserDefaultUILanguage() }))
}

#[cfg(not(target_os = "windows"))]
pub fn detect() -> Option<&'static str> {
    None
}

#[cfg(any(target_os = "windows", test))]
fn locale_for_language_id(language: u16) -> &'static str {
    // PRIMARYLANGID: Chinese (0x04), regardless of region or script.
    if language & 0x03ff == 0x04 { "zh-CN" } else { "en" }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn all_chinese_regions_use_chinese() {
        for language in [0x0804, 0x0404, 0x0c04, 0x1004, 0x1404] {
            assert_eq!(locale_for_language_id(language), "zh-CN");
        }
    }

    #[test]
    fn english_and_other_languages_use_english() {
        for language in [0x0409, 0x0809, 0x040c, 0x0411, 0x0407, 0] {
            assert_eq!(locale_for_language_id(language), "en");
        }
    }
}

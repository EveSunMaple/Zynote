use std::env;
use std::io::{BufRead, Write};

#[derive(Debug)]
enum Mode {
    Pdf,
    Asr,
    Youtube,
    Wechat,
}

fn parse_mode() -> Result<Mode, String> {
    let args: Vec<String> = env::args().collect();
    let index = args
        .iter()
        .position(|arg| arg == "--mode")
        .ok_or("missing --mode")?;
    let value = args.get(index + 1).ok_or("missing --mode value")?;
    match value.as_str() {
        "pdf" => Ok(Mode::Pdf),
        "asr" => Ok(Mode::Asr),
        "youtube" => Ok(Mode::Youtube),
        "wechat" => Ok(Mode::Wechat),
        other => Err(format!("unknown mode {other}")),
    }
}

fn main() {
    let mode = match parse_mode() {
        Ok(mode) => mode,
        Err(message) => {
            eprintln!("zynote-native: {message}");
            std::process::exit(1);
        }
    };
    let stdin = std::io::stdin();
    let stdout = std::io::stdout();
    let mut out = stdout.lock();
    let mut input = stdin.lock();
    let mut line = String::new();
    loop {
        line.clear();
        match input.read_line(&mut line) {
            Ok(0) => break,
            Ok(_) => {}
            Err(error) => {
                eprintln!("zynote-native: stdin error {error}");
                std::process::exit(1);
            }
        }
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }
        let method = json_string_field(trimmed, "method").unwrap_or_default();
        let id = json_string_field(trimmed, "id").unwrap_or_default();
        if method.is_empty() {
            continue;
        }
        let result = match method.as_str() {
            "core.hello" => {
                format!(r#"{{"protocol":1,"mode":"{}","pid":{}}}"#, mode_name(&mode), std::process::id())
            }
            "ping" => r#"{"pong":true}"#.to_string(),
            other => {
                eprintln!("zynote-native: mode {} does not implement {other}", mode_name(&mode));
                continue;
            }
        };
        let reply = if id.is_empty() {
            format!(r#"{{"jsonrpc":"2.0","method":"event","params":{}}}"#, result)
        } else {
            format!(r#"{{"jsonrpc":"2.0","id":{},"result":{}}}"#,
                serde_json_string(&id), result)
        };
        if writeln!(out, "{reply}").is_err() {
            break;
        }
    }
}

fn mode_name(mode: &Mode) -> &'static str {
    match mode {
        Mode::Pdf => "pdf",
        Mode::Asr => "asr",
        Mode::Youtube => "youtube",
        Mode::Wechat => "wechat",
    }
}

fn json_string_field(input: &str, field: &str) -> Option<String> {
    let key = format!("\"{field}\"");
    let start = input.find(&key)? + key.len();
    let rest = input[start..].trim_start();
    if !rest.starts_with(':') {
        return None;
    }
    let rest = rest[1..].trim_start();
    let mut chars = rest.chars();
    if chars.next()? != '"' {
        return None;
    }
    let mut value = String::new();
    for char in chars {
        if char == '"' {
            return Some(value);
        }
        value.push(char);
    }
    None
}

fn serde_json_string(value: &str) -> String {
    if value.starts_with('"') && value.ends_with('"') {
        value.to_string()
    } else {
        format!("\"{value}\"")
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_string_field() {
        assert_eq!(
            json_string_field(r#"{"jsonrpc":"2.0","id":"a","method":"ping"}"#, "method"),
            Some("ping".to_string())
        );
        assert_eq!(json_string_field(r#"{"method":"ping"}"#, "nope"), None);
    }
}

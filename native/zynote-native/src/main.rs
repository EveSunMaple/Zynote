use serde::Deserialize;
use serde_json::{json, Value};
use std::io::{BufRead, Write};

mod pdf;

#[derive(Debug, Clone)]
enum Mode {
    Pdf,
    Asr,
    Youtube,
    Wechat,
}

fn parse_mode() -> Result<(Mode, Option<String>), String> {
    let args: Vec<String> = std::env::args().collect();
    let index = args
        .iter()
        .position(|arg| arg == "--mode")
        .ok_or("missing --mode")?;
    let value = args.get(index + 1).ok_or("missing --mode value")?;
    let pdfium = args
        .iter()
        .position(|arg| arg == "--pdfium")
        .and_then(|i| args.get(i + 1).cloned());
    let mode = match value.as_str() {
        "pdf" => Mode::Pdf,
        "asr" => Mode::Asr,
        "youtube" => Mode::Youtube,
        "wechat" => Mode::Wechat,
        other => return Err(format!("unknown mode {other}")),
    };
    Ok((mode, pdfium))
}

#[derive(Deserialize)]
struct Envelope {
    #[serde(default)]
    id: Option<Value>,
    #[serde(default)]
    method: String,
    #[serde(default)]
    params: Value,
}

fn main() {
    let (mode, pdfium_path) = match parse_mode() {
        Ok(result) => result,
        Err(message) => {
            eprintln!("zynote-native: {message}");
            std::process::exit(1);
        }
    };
    let pdf = match (&mode, pdfium_path) {
        (Mode::Pdf, Some(path)) => match pdf::PdfService::new(&path) {
            Ok(service) => Some(service),
            Err(error) => {
                eprintln!("zynote-native: pdfium bind failed: {error}");
                std::process::exit(1);
            }
        },
        _ => None,
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
        let envelope: Envelope = match serde_json::from_str(trimmed) {
            Ok(value) => value,
            Err(_) => continue,
        };
        if envelope.method.is_empty() {
            continue;
        }
        let id = envelope.id.clone();
        let result = dispatch(&mode, &pdf, &envelope.method, &envelope.params);
        let reply = match (id, result) {
            (Some(id), Ok(result)) => json!({"jsonrpc": "2.0", "id": id, "result": result}),
            (Some(id), Err(message)) => {
                json!({"jsonrpc": "2.0", "id": id, "error": {"code": "INPUT_INVALID", "message": message}})
            }
            (None, _) => continue,
        };
        if writeln!(out, "{reply}").is_err() {
            break;
        }
    }
}

fn dispatch(mode: &Mode, pdf: &Option<pdf::PdfService>, method: &str, params: &Value) -> Result<Value, String> {
    match (mode, method) {
        (_, "core.hello") => Ok(json!({
            "protocol": 1,
            "mode": mode_name(mode),
            "pid": std::process::id()
        })),
        (_, "ping") => Ok(json!({"pong": true})),
        (Mode::Pdf, "pdf.text") => {
            let path = params
                .get("path")
                .and_then(|value| value.as_str())
                .ok_or("path missing")?;
            let page = params
                .get("page")
                .and_then(|value| value.as_u64())
                .map(|value| value as usize);
            pdf.as_ref().ok_or("pdfium not bound")?.text(path, page)
        }
        (Mode::Pdf, "pdf.plainText") => {
            let path = params
                .get("path")
                .and_then(|value| value.as_str())
                .ok_or("path missing")?;
            pdf.as_ref().ok_or("pdfium not bound")?.plain_text(path)
        }
        (mode, other) => Err(format!("mode {} does not implement {other}", mode_name(mode))),
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

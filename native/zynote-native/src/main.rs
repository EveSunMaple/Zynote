use serde::Deserialize;
use serde_json::{json, Value};
use std::io::{BufRead, Write};

mod annotations;
mod asr;
mod embed;
mod pdf;
mod youtube;

#[derive(Debug, Clone)]
enum Mode {
    Pdf,
    Asr,
    Youtube,
    Wechat,
    Embed,
}

fn parse_mode() -> Result<(Mode, Option<String>, Option<String>, Option<String>, Option<String>), String> {
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
    let youtube_cache = args
        .iter()
        .position(|arg| arg == "--youtube-cache")
        .and_then(|i| args.get(i + 1).cloned());
    let embed_model = args
        .iter()
        .position(|arg| arg == "--embed-model")
        .and_then(|i| args.get(i + 1).cloned());
    let asr_model = args
        .iter()
        .position(|arg| arg == "--asr-model")
        .and_then(|i| args.get(i + 1).cloned());
    let mode = match value.as_str() {
        "pdf" => Mode::Pdf,
        "asr" => Mode::Asr,
        "youtube" => Mode::Youtube,
        "wechat" => Mode::Wechat,
        "embed" => Mode::Embed,
        other => return Err(format!("unknown mode {other}")),
    };
    Ok((mode, pdfium, youtube_cache, embed_model, asr_model))
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
    let (mode, pdfium_path, youtube_cache, embed_model, asr_model) = match parse_mode() {
        Ok(result) => result,
        Err(message) => {
            eprintln!("zynote-native: {message}");
            std::process::exit(1);
        }
    };
    let youtube_service = if matches!(mode, Mode::Youtube) {
        match youtube::YoutubeService::new(youtube_cache.as_deref()) {
            Ok(service) => Some(service),
            Err(error) => {
                eprintln!("zynote-native: {error}");
                std::process::exit(1);
            }
        }
    } else {
        None
    };
    let embed_service = if matches!(mode, Mode::Embed) {
        match embed_model.as_deref() {
            Some(path) => match embed::EmbedService::new(path) {
                Ok(service) => Some(service),
                Err(error) => {
                    eprintln!("zynote-native: {error}");
                    std::process::exit(1);
                }
            },
            None => {
                eprintln!("zynote-native: missing --embed-model");
                std::process::exit(1);
            }
        }
    } else {
        None
    };
    let asr_service = if matches!(mode, Mode::Asr) {
        match asr_model.as_deref() {
            Some(path) => match asr::AsrService::new(path) {
                Ok(service) => Some(service),
                Err(error) => {
                    eprintln!("zynote-native: {error}");
                    std::process::exit(1);
                }
            },
            None => {
                eprintln!("zynote-native: missing --asr-model");
                std::process::exit(1);
            }
        }
    } else {
        None
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
        let result = dispatch(
            &mode,
            &pdf,
            &youtube_service,
            &embed_service,
            &asr_service,
            &envelope.method,
            &envelope.params,
        );
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

fn dispatch(
    mode: &Mode,
    pdf: &Option<pdf::PdfService>,
    youtube: &Option<youtube::YoutubeService>,
    embed: &Option<embed::EmbedService>,
    asr: &Option<asr::AsrService>,
    method: &str,
    params: &Value,
) -> Result<Value, String> {
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
        (Mode::Pdf, "pdf.inspect") => {
            let path = params
                .get("path")
                .and_then(|value| value.as_str())
                .ok_or("path missing")?;
            annotations::inspect_pdf(path)
        }
        (Mode::Pdf, "pdf.write") => {
            let path = params
                .get("path")
                .and_then(|value| value.as_str())
                .ok_or("path missing")?;
            let output = params
                .get("output")
                .and_then(|value| value.as_str())
                .ok_or("output missing")?;
            let rows = params.get("rows").cloned().unwrap_or(Value::Array(Vec::new()));
            annotations::write_annotations(path, output, &rows)
        }
        (Mode::Youtube, "youtube.video") => {
            let id = params
                .get("id")
                .and_then(|value| value.as_str())
                .ok_or("id missing")?;
            youtube.as_ref().ok_or("youtube service not ready")?.video(id)
        }
        (Mode::Youtube, "youtube.playlist") => {
            let id = params
                .get("id")
                .and_then(|value| value.as_str())
                .ok_or("id missing")?;
            youtube.as_ref().ok_or("youtube service not ready")?.playlist(id)
        }
        (Mode::Embed, "embed.text") => {
            let text = params
                .get("text")
                .and_then(|value| value.as_str())
                .ok_or("text missing")?;
            let query = params.get("query").and_then(|value| value.as_bool()).unwrap_or(false);
            let vector = embed
                .as_ref()
                .ok_or("embed service not ready")?
                .encode(text, query)?;
            Ok(json!({"vector": vector}))
        }
        (Mode::Embed, "embed.batch") => {
            let texts = params
                .get("texts")
                .and_then(|value| value.as_array())
                .ok_or("texts missing")?
                .iter()
                .map(|value| value.as_str().unwrap_or("").to_string())
                .collect::<Vec<String>>();
            let query = params.get("query").and_then(|value| value.as_bool()).unwrap_or(false);
            let vectors = embed
                .as_ref()
                .ok_or("embed service not ready")?
                .encode_batch(&texts, query)?;
            Ok(json!({"vectors": vectors}))
        }
        (Mode::Asr, "asr.transcribe") => {
            let path = params
                .get("path")
                .and_then(|value| value.as_str())
                .ok_or("path missing")?;
            let language = params
                .get("language")
                .and_then(|value| value.as_str())
                .unwrap_or("auto");
            let (duration, cues) = asr
                .as_ref()
                .ok_or("asr service not ready")?
                .transcribe(path, language)?;
            Ok(json!({"duration": duration, "cues": cues}))
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
        Mode::Embed => "embed",
    }
}

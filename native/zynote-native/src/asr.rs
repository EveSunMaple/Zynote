use std::sync::Mutex;

pub struct AsrService {
    transcriber: Mutex<docling_asr::whisper::Transcriber>,
}

impl AsrService {
    pub fn new(directory: &str) -> Result<Self, String> {
        std::env::set_var("DOCLING_ASR_ENCODER", format!("{directory}/onnx/encoder_model_fp16.onnx"));
        std::env::set_var("DOCLING_ASR_DECODER", format!("{directory}/onnx/decoder_model_quantized.onnx"));
        std::env::set_var("DOCLING_ASR_VOCAB", format!("{directory}/vocab.json"));
        std::env::set_var(
            "DOCLING_ASR_ADDED_TOKENS",
            format!("{directory}/added_tokens.json"),
        );
        let transcriber = docling_asr::whisper::Transcriber::load()
            .map_err(|error| format!("本地字幕模型载入失败：{error}"))?;
        Ok(Self {
            transcriber: Mutex::new(transcriber),
        })
    }

    pub fn transcribe(&self, path: &str, language: &str) -> Result<(f64, Vec<serde_json::Value>), String> {
        let info = std::fs::metadata(path).map_err(|error| format!("音频文件不可读：{error}"))?;
        if !info.is_file() || info.len() > 512 * 1024 * 1024 || info.len() % 4 != 0 {
            return Err("音频文件无效".to_string());
        }
        let bytes = std::fs::read(path).map_err(|error| format!("音频文件不可读：{error}"))?;
        let samples: Vec<f32> = bytes
            .chunks_exact(4)
            .map(|chunk| f32::from_le_bytes([chunk[0], chunk[1], chunk[2], chunk[3]]))
            .collect();
        if samples.is_empty() || samples.iter().any(|value| !value.is_finite()) {
            return Err("音频采样无效".to_string());
        }
        let language = if language.is_empty() { "auto" } else { language };
        let mut transcriber = self
            .transcriber
            .lock()
            .map_err(|_| "字幕模型会话不可用".to_string())?;
        transcriber
            .set_language(language)
            .map_err(|error| format!("字幕语言无效：{error}"))?;
        let segments = transcriber
            .transcribe(&samples)
            .map_err(|error| format!("转写失败：{error}"))?;
        let cues = segments
            .into_iter()
            .map(|segment| {
                serde_json::json!({
                    "from": segment.start, "to": segment.end, "content": segment.text
                })
            })
            .collect::<Vec<serde_json::Value>>();
        if cues.is_empty() {
            return Err("未识别到语音，原视频保留".to_string());
        }
        Ok((samples.len() as f64 / 16_000.0, cues))
    }
}

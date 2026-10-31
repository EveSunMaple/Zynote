use ort::session::Session;
use ort::value::Tensor;
use std::collections::HashMap;
use std::sync::Mutex;

const SAMPLE_RATE: usize = 16_000;
const FRAME: usize = 512;
const CONTEXT: usize = 64;
const BATCH: usize = 10_000;

// Whisper decodes zero-padded 30-second windows. A valid speech cue may end
// inside that padding, especially for sub-second VAD ranges. The actual audio
// boundary is authoritative; never extend the recording to a model timestamp.
fn bounded_span(from: f64, to: f64, duration: f64) -> Result<(f64, f64), String> {
    if !from.is_finite()
        || !to.is_finite()
        || !duration.is_finite()
        || duration <= 0.0
        || from < 0.0
        || to <= from
        || from >= duration
        || to > duration + 30.0
    {
        return Err(format!("转写结果缺少可靠的字幕时间点：from={from:.3}, to={to:.3}, range_duration={duration:.3}"));
    }
    Ok((from, to.min(duration)))
}

pub struct AsrService {
    transcriber: Mutex<docling_asr::whisper::Transcriber>,
    vad: Mutex<Session>,
    vad_inputs: Vec<String>,
    vad_outputs: Vec<String>,
}

pub fn speech_ranges(probabilities: &[f32], samples: usize) -> Result<Vec<(usize, usize)>, String> {
    let mut ranges: Vec<(usize, usize)> = Vec::new();
    let mut start: Option<usize> = None;
    let mut silence: Option<usize> = None;
    for (index, &probability) in probabilities.iter().enumerate() {
        if !probability.is_finite() || !(0.0..=1.0).contains(&probability) {
            return Err("语音检测返回无效概率".to_string());
        }
        let position = index * FRAME;
        if probability >= 0.5 {
            silence = None;
            if start.is_none() {
                start = Some(position);
            }
        } else if probability < 0.35 {
            if let Some(value) = start {
                if silence.is_none() {
                    silence = Some(position);
                }
                if position - silence.unwrap() >= 32_000 {
                    if silence.unwrap() > value {
                        ranges.push((value, silence.unwrap()));
                    }
                    start = None;
                    silence = None;
                }
            }
        }
    }
    if let Some(value) = start {
        if samples > value {
            ranges.push((value, samples));
        }
    }
    for index in 0..ranges.len() {
        if index == 0 {
            ranges[index].0 = ranges[index].0.saturating_sub(6_400);
        }
        if index + 1 < ranges.len() {
            let next_start = ranges[index + 1].0;
            let gap = next_start as i64 - ranges[index].1 as i64;
            if gap < 12_800 {
                let half = (gap.max(0) / 2) as usize;
                ranges[index].1 += half;
                ranges[index + 1].0 = next_start.saturating_sub(half);
            } else {
                ranges[index].1 = samples.min(ranges[index].1 + 6_400);
                ranges[index + 1].0 = next_start.saturating_sub(6_400);
            }
        } else {
            ranges[index].1 = samples.min(ranges[index].1 + 6_400);
        }
    }
    Ok(ranges)
}

impl AsrService {
    pub fn new(directory: &str) -> Result<Self, String> {
        std::env::set_var(
            "DOCLING_ASR_ENCODER",
            format!("{directory}/onnx/encoder_model_fp16.onnx"),
        );
        std::env::set_var(
            "DOCLING_ASR_DECODER",
            format!("{directory}/onnx/decoder_model_quantized.onnx"),
        );
        std::env::set_var("DOCLING_ASR_VOCAB", format!("{directory}/vocab.json"));
        std::env::set_var(
            "DOCLING_ASR_ADDED_TOKENS",
            format!("{directory}/added_tokens.json"),
        );
        let transcriber = docling_asr::whisper::Transcriber::load()
            .map_err(|error| format!("本地字幕模型载入失败：{error}"))?;
        let vad = docling_onnx::session_builder()
            .map_err(|error| format!("语音检测会话创建失败：{error}"))?
            .with_intra_threads(1)
            .map_err(|error| format!("语音检测线程配置失败：{error}"))?
            .with_inter_threads(1)
            .map_err(|error| format!("语音检测线程配置失败：{error}"))?
            .commit_from_file(format!("{directory}/silero-vad-v6.onnx"))
            .map_err(|error| format!("语音检测模型载入失败：{error}"))?;
        let vad_inputs = vad
            .inputs()
            .iter()
            .map(|input| input.name().to_string())
            .collect::<Vec<String>>();
        let vad_outputs = vad
            .outputs()
            .iter()
            .map(|output| output.name().to_string())
            .collect::<Vec<String>>();
        if vad_inputs.len() != 3 || vad_outputs.len() < 3 {
            return Err("语音检测模型结构不符".to_string());
        }
        Ok(Self {
            transcriber: Mutex::new(transcriber),
            vad: Mutex::new(vad),
            vad_inputs,
            vad_outputs,
        })
    }

    fn detect_speech(&self, samples: &[f32]) -> Result<Vec<(usize, usize)>, String> {
        let mut session = self
            .vad
            .lock()
            .map_err(|_| "语音检测会话不可用".to_string())?;
        let mut h = vec![0.0f32; 128];
        let mut c = vec![0.0f32; 128];
        let mut probabilities: Vec<f32> = Vec::new();
        let total = samples.len().div_ceil(FRAME);
        let mut first = 0;
        while first < total {
            let count = BATCH.min(total - first);
            let mut input = vec![0.0f32; count * 576];
            for row in 0..count {
                let start = (first + row) * FRAME;
                let offset = row * 576;
                if start > 0 {
                    let from = start.saturating_sub(CONTEXT);
                    let context = &samples[from..start];
                    input[offset..offset + context.len()].copy_from_slice(context);
                }
                if start < samples.len() {
                    let end = samples.len().min(start + FRAME);
                    input[offset + CONTEXT..offset + CONTEXT + (end - start)]
                        .copy_from_slice(&samples[start..end]);
                }
            }
            let mut inputs: HashMap<String, Tensor<f32>> = HashMap::new();
            let shape = vec![count as i64, 576];
            let frame_shape = vec![1_i64, 1, 128];
            inputs.insert(
                self.vad_inputs[0].clone(),
                Tensor::from_array((shape, input))
                    .map_err(|error| format!("语音检测输入无效：{error}"))?,
            );
            inputs.insert(
                self.vad_inputs[1].clone(),
                Tensor::from_array((frame_shape.clone(), std::mem::take(&mut h)))
                    .map_err(|error| format!("语音检测输入无效：{error}"))?,
            );
            inputs.insert(
                self.vad_inputs[2].clone(),
                Tensor::from_array((frame_shape, std::mem::take(&mut c)))
                    .map_err(|error| format!("语音检测输入无效：{error}"))?,
            );
            let outputs = session
                .run(inputs)
                .map_err(|error| format!("语音检测推理失败：{error}"))?;
            let (_, values) = outputs[self.vad_outputs[0].as_str()]
                .try_extract_tensor::<f32>()
                .map_err(|error| format!("语音检测输出无效：{error}"))?;
            if values.len() != count {
                return Err("语音检测输出维度不符".to_string());
            }
            probabilities.extend_from_slice(values);
            let (_, next_h) = outputs[self.vad_outputs[1].as_str()]
                .try_extract_tensor::<f32>()
                .map_err(|error| format!("语音检测状态无效：{error}"))?;
            let (_, next_c) = outputs[self.vad_outputs[2].as_str()]
                .try_extract_tensor::<f32>()
                .map_err(|error| format!("语音检测状态无效：{error}"))?;
            if next_h.len() != 128 || next_c.len() != 128 {
                return Err("语音检测状态维度不符".to_string());
            }
            h = next_h.to_vec();
            c = next_c.to_vec();
            first += count;
        }
        speech_ranges(&probabilities, samples.len())
    }

    pub fn transcribe(
        &self,
        path: &str,
        language: &str,
    ) -> Result<(f64, Vec<serde_json::Value>), String> {
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
        let ranges = self.detect_speech(&samples)?;
        if ranges.is_empty() {
            return Err("未检测到语音，原视频保留以便检查".to_string());
        }
        let language = if language.is_empty() {
            "auto"
        } else {
            language
        };
        let mut transcriber = self
            .transcriber
            .lock()
            .map_err(|_| "字幕模型会话不可用".to_string())?;
        transcriber
            .set_language(language)
            .map_err(|error| format!("字幕语言无效：{error}"))?;
        let mut cues = Vec::new();
        for (start, end) in ranges {
            let duration = (end - start) as f64 / SAMPLE_RATE as f64;
            let offset = start as f64 / SAMPLE_RATE as f64;
            let segments = transcriber
                .transcribe(&samples[start..end])
                .map_err(|error| format!("转写失败：{error}"))?;
            for segment in segments {
                let text = segment.text.trim();
                if text.is_empty() {
                    continue;
                }
                let (from, to) = bounded_span(segment.start, segment.end, duration)?;
                cues.push(serde_json::json!({
                    "from": from + offset,
                    "to": to + offset,
                    "content": text
                }));
            }
        }
        if cues.is_empty() {
            return Err("未识别到语音，原视频保留".to_string());
        }
        Ok((samples.len() as f64 / SAMPLE_RATE as f64, cues))
    }
}

#[cfg(test)]
mod tests {
    use super::{bounded_span, speech_ranges};

    #[test]
    fn padded_short_speech_ends_at_real_audio_boundary() {
        assert_eq!(bounded_span(0.0, 2.0, 0.832).unwrap(), (0.0, 0.832));
        assert_eq!(bounded_span(1.0, 2.0, 3.0).unwrap(), (1.0, 2.0));
    }

    #[test]
    fn invalid_spans_are_not_repaired_into_fake_timestamps() {
        for (from, to, duration) in [
            (1.0, 2.0, 0.832),
            (0.0, 0.0, 1.0),
            (-1.0, 1.0, 2.0),
            (0.0, f64::NAN, 2.0),
            (f64::INFINITY, 2.0, 3.0),
            (0.0, 50.0, 0.832),
            (0.0, 2.0, 0.0),
        ] {
            assert!(bounded_span(from, to, duration).is_err());
        }
    }

    #[test]
    fn silence_has_no_ranges() {
        assert!(speech_ranges(&[0.0; 64], 32_768).unwrap().is_empty());
    }

    #[test]
    fn single_burst_is_padded() {
        let mut probabilities = vec![0.0f32; 64];
        for value in probabilities.iter_mut().take(20).skip(10) {
            *value = 0.9;
        }
        let ranges = speech_ranges(&probabilities, 32_768).unwrap();
        assert_eq!(ranges.len(), 1);
        assert_eq!(ranges[0].0, 0);
        assert_eq!(ranges[0].1, 32_768);
    }

    #[test]
    fn long_silence_splits_ranges() {
        let mut probabilities = vec![0.0f32; 200];
        for value in probabilities.iter_mut().take(20).skip(10) {
            *value = 0.9;
        }
        for value in probabilities.iter_mut().take(150).skip(90) {
            *value = 0.9;
        }
        let ranges = speech_ranges(&probabilities, 102_400).unwrap();
        assert_eq!(ranges.len(), 2);
        assert_eq!(ranges[0].0, 0);
        assert!(ranges[0].1 > 10 * 512);
        assert!(ranges[1].0 >= 90 * 512 - 6_400);
        assert_eq!(ranges[1].1, 102_400);
    }

    #[test]
    fn separated_ranges_are_padded_without_overlap() {
        let mut probabilities = vec![0.0f32; 100];
        for value in probabilities.iter_mut().take(30).skip(20) {
            *value = 0.9;
        }
        for value in probabilities.iter_mut().take(100).skip(95) {
            *value = 0.9;
        }
        let ranges = speech_ranges(&probabilities, 51_200).unwrap();
        assert_eq!(ranges.len(), 2);
        assert_eq!(ranges[0].0, 20 * 512 - 6_400);
        assert_eq!(ranges[0].1, 30 * 512 + 6_400);
        assert_eq!(ranges[1].0, 95 * 512 - 6_400);
        assert_eq!(ranges[1].1, 51_200);
    }

    #[test]
    fn invalid_probability_is_rejected() {
        assert!(speech_ranges(&[f32::NAN], 512).is_err());
        assert!(speech_ranges(&[1.2], 512).is_err());
    }
}

use ort::session::Session;
use ort::value::Tensor;
use std::collections::HashMap;
use std::sync::Mutex;
use tokenizers::{Tokenizer, TruncationDirection, TruncationParams, TruncationStrategy};

pub struct EmbedService {
    tokenizer: Tokenizer,
    session: Mutex<Session>,
    inputs: Vec<String>,
    output: String,
}

const QUERY_PREFIX: &str = "为这个句子生成表示以用于检索相关文章：";

impl EmbedService {
    pub fn new(directory: &str) -> Result<Self, String> {
        let mut tokenizer = Tokenizer::from_file(format!("{directory}/tokenizer.json"))
            .map_err(|error| format!("语义模型分词器不可用：{error}"))?;
        tokenizer
            .with_truncation(Some(TruncationParams {
                max_length: 512,
                stride: 0,
                strategy: TruncationStrategy::LongestFirst,
                direction: TruncationDirection::Right,
            }))
            .map_err(|error| format!("语义模型分词器配置失败：{error}"))?;
        let environment = ort::init()
            .with_name("zynote-embed")
            .build()
            .map_err(|error| format!("ONNX 运行环境初始化失败：{error}"))?;
        let session = Session::builder(&environment)
            .map_err(|error| format!("语义模型会话创建失败：{error}"))?
            .with_intra_threads(4)
            .map_err(|error| format!("语义模型线程配置失败：{error}"))?
            .commit_from_file(format!("{directory}/onnx/model_quantized.onnx"))
            .map_err(|error| format!("语义模型载入失败：{error}"))?;
        let inputs = session
            .inputs()
            .iter()
            .map(|input| input.name().to_string())
            .collect::<Vec<String>>();
        let output = session
            .outputs()
            .iter()
            .map(|output| output.name().to_string())
            .find(|name| name == "last_hidden_state")
            .or_else(|| session.outputs().first().map(|output| output.name().to_string()))
            .ok_or("语义模型缺少输出")?;
        Ok(Self {
            tokenizer,
            session: Mutex::new(session),
            inputs,
            output,
        })
    }

    pub fn encode(&self, text: &str, query: bool) -> Result<Vec<f32>, String> {
        if text.is_empty() {
            return Err("语义检索文字为空".to_string());
        }
        let points: Vec<char> = text.chars().collect();
        let mut combined = [0.0f32; 512];
        let mut windows = 0;
        let mut offset = 0;
        while offset < points.len() {
            let end = (offset + 400).min(points.len());
            let window: String = points[offset..end].iter().collect();
            let value = if query {
                format!("{QUERY_PREFIX}{window}")
            } else {
                window
            };
            let encoding = self
                .tokenizer
                .encode(value, true)
                .map_err(|error| format!("语义模型分词失败：{error}"))?;
            if encoding.is_empty() {
                return Err("语义检索文字为空".to_string());
            }
            let ids = encoding.get_ids().iter().map(|&id| id as i64).collect::<Vec<i64>>();
            let mask = encoding
                .get_attention_mask()
                .iter()
                .map(|&value| value as i64)
                .collect::<Vec<i64>>();
            let types = encoding
                .get_type_ids()
                .iter()
                .map(|&value| value as i64)
                .collect::<Vec<i64>>();
            let shape = vec![1_i64, ids.len() as i64];
            let mut inputs: HashMap<String, Tensor<i64>> = HashMap::new();
            for name in &self.inputs {
                let data = match name.as_str() {
                    "input_ids" => ids.clone(),
                    "attention_mask" => mask.clone(),
                    "token_type_ids" => types.clone(),
                    _ => return Err(format!("语义模型包含未知输入：{name}")),
                };
                inputs.insert(
                    name.clone(),
                    Tensor::from_array((shape.clone(), data))
                        .map_err(|error| format!("语义模型输入无效：{error}"))?,
                );
            }
            let mut session = self
                .session
                .lock()
                .map_err(|_| "语义模型会话不可用".to_string())?;
            let outputs = session
                .run(inputs)
                .map_err(|error| format!("语义模型推理失败：{error}"))?;
            let (dimensions, data) = outputs[self.output.as_str()]
                .try_extract_tensor::<f32>()
                .map_err(|error| format!("语义模型输出无效：{error}"))?;
            if dimensions.num_elements() < 512 {
                return Err("中文语义模型维度不符".to_string());
            }
            for index in 0..512 {
                combined[index] += data[index];
            }
            windows += 1;
            offset += 320;
        }
        if windows == 0 {
            return Err("语义检索文字为空".to_string());
        }
        let length = combined.iter().map(|value| value * value).sum::<f32>().sqrt();
        if !length.is_finite() || length <= 0.0 {
            return Err("语义向量无效".to_string());
        }
        Ok(combined.iter().map(|value| value / length).collect())
    }

    pub fn encode_batch(&self, texts: &[String], query: bool) -> Result<Vec<Vec<f32>>, String> {
        let mut result = Vec::with_capacity(texts.len());
        for text in texts {
            result.push(self.encode(text, query)?);
        }
        Ok(result)
    }
}

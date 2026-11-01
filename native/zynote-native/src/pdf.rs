use pdfium_render::prelude::*;
use serde_json::{json, Value};
use std::path::Path;

pub struct PdfService {
    pdfium: Pdfium,
}

impl PdfService {
    pub fn new(library_path: &str) -> Result<Self, String> {
        let bindings =
            Pdfium::bind_to_library(library_path).map_err(|error| format!("pdfium 绑定失败: {error}"))?;
        Ok(Self { pdfium: Pdfium::new(bindings) })
    }

    fn load(&self, path: &str) -> Result<PdfDocument, String> {
        if !Path::new(path).is_file() {
            return Err(format!("文件不存在: {path}"));
        }
        self.pdfium
            .load_pdf_from_file(path, None)
            .map_err(|error| format!("PDF 无法打开: {error}"))
    }

    fn segment_items(page_text: &PdfPageText) -> Vec<Value> {
        let mut items = Vec::new();
        for segment in page_text.segments().iter() {
            let value = segment.text();
            if value.trim().is_empty() {
                continue;
            }
            let bounds = segment.bounds();
            items.push(json!({
                "text": value,
                "x": bounds.left.value as f64,
                "y": bounds.bottom.value as f64,
                "width": (bounds.right.value - bounds.left.value) as f64,
                "height": (bounds.top.value - bounds.bottom.value) as f64
            }));
        }
        items
    }

    pub fn text(&self, path: &str, page_selected: Option<usize>) -> Result<Value, String> {
        let document = self.load(path)?;
        let num_pages = document.pages().len() as usize;
        let mut pages = Vec::new();
        for (offset, page) in document.pages().iter().enumerate() {
            let one_based = offset + 1;
            if let Some(selected) = page_selected {
                if one_based != selected {
                    continue;
                }
            }
            let page_text = page.text().map_err(|error| format!("{error}"))?;
            pages.push(json!({"index": one_based, "items": Self::segment_items(&page_text)}));
        }
        Ok(json!({"numPages": num_pages, "pages": pages}))
    }

    pub fn plain_text(&self, path: &str) -> Result<Value, String> {
        let document = self.load(path)?;
        let num_pages = document.pages().len() as usize;
        let mut text = String::new();
        let mut real_count = 0usize;
        for (offset, page) in document.pages().iter().enumerate() {
            let page_text = page.text().map_err(|error| format!("{error}"))?;
            let items = Self::segment_items(&page_text);
            let mut parts = Vec::new();
            for item in &items {
                parts.push(item.get("text").and_then(|value| value.as_str()).unwrap_or_default().to_string());
            }
            text += &format!("\n[第 {} 页]\n", offset + 1);
            text += &parts.join(" ");
            real_count += parts.join("").chars().filter(|c| !c.is_whitespace()).count();
        }
        let needs_ocr = real_count < std::cmp::max(20, num_pages * 10);
        Ok(json!({"text": text, "needsOcr": needs_ocr, "numPages": num_pages}))
    }
}

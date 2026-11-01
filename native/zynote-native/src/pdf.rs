use base64::{engine::general_purpose::STANDARD, Engine};
use image::codecs::jpeg::JpegEncoder;
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

    pub fn render_page(&self, path: &str, number: u64) -> Result<Value, String> {
        let document = self.load(path)?;
        let pages = document.pages();
        let index = number
            .checked_sub(1)
            .and_then(|n| u16::try_from(n).ok())
            .filter(|&n| n < pages.len())
            .ok_or("教材页码无效")?;
        let page = pages.get(index).map_err(|_| "教材页面不可读")?;
        let width = page.width().value;
        let height = page.height().value;
        if !width.is_finite() || !height.is_finite() || width <= 0.0 || height <= 0.0 {
            return Err("教材页面尺寸无效".into());
        }
        let rotation = page.rotation().map_err(|_| "教材页面方向不可读")?.as_degrees();
        let scale = 1400.0 / width.max(height);
        let config = PdfRenderConfig::new().set_target_size(
            (width * scale).round().clamp(1.0, 1400.0) as i32,
            (height * scale).round().clamp(1.0, 1400.0) as i32,
        );
        let bitmap = page.render_with_config(&config).map_err(|_| "教材页面渲染失败")?;
        // Use the exact viewport used for the JPEG, including the crop origin and intrinsic rotation.
        let to_pdf = |x, y| page.pixels_to_points(x, y, &config).map_err(|_| "教材页面坐标换算失败");
        let (x0, y0) = to_pdf(0, 0)?;
        let (xu, yu) = to_pdf(bitmap.width(), 0)?;
        let (xv, yv) = to_pdf(0, bitmap.height())?;
        let rgb = bitmap.as_image().into_rgb8();
        let mut jpeg = Vec::new();
        JpegEncoder::new_with_quality(&mut jpeg, 83)
            .encode_image(&rgb)
            .map_err(|_| "教材页面图像编码失败")?;
        // Base64 expands by 4/3. Leave room in the 8 MiB helper reply for all metadata.
        if jpeg.len() > 5 * 1024 * 1024 {
            return Err("单页图片超过识别上限".into());
        }
        Ok(json!({
            "image": format!("data:image/jpeg;base64,{}", STANDARD.encode(jpeg)),
            "size": [width, height, rotation],
            "imageToPdf": [x0.value, y0.value, (xu - x0).value, (yu - y0).value, (xv - x0).value, (yv - y0).value],
            "pages": pages.len()
        }))
    }

    pub fn text(&self, path: &str, page_selected: Option<usize>, glyphs: bool) -> Result<Value, String> {
        if glyphs && page_selected.is_none() {
            return Err("字形坐标仅支持按页读取".into());
        }
        let document = self.load(path)?;
        let num_pages = document.pages().len() as usize;
        let mut pages = Vec::new();
        let indices = match page_selected {
            Some(selected) if (1..=num_pages).contains(&selected) => selected..=selected,
            Some(_) => return Err("PDF 页码越界".to_string()),
            None => 1..=num_pages,
        };
        for one_based in indices {
            let page = document
                .pages()
                .get((one_based - 1) as PdfPageIndex)
                .map_err(|error| format!("{error}"))?;
            let page_text = page.text().map_err(|error| format!("{error}"))?;
            if glyphs {
                let rect = page.boundaries().bounding().map_err(|_| "教材页面边界不可读")?.bounds;
                let view = [rect.left().value, rect.bottom().value, rect.right().value, rect.top().value];
                pages.push(json!({
                    "index": one_based, "viewBox": view,
                    "hasBodyImage": crate::pdf_body::has_body_image(&page, view),
                    "items": crate::pdf_glyphs::lines(&page_text, view)?
                }));
            } else {
                pages.push(json!({"index": one_based, "items": Self::segment_items(&page_text)}));
            }
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

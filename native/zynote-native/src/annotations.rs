use lopdf::{Document as LopdfDocument, Object, ObjectId, StringFormat};
use serde_json::{json, Map as JsonMap, Value};

const LEGACY_PREFIX: &str = "meijiang:";
const CURRENT_PREFIX: &str = "zynote:";
const LEGACY_DATA_KEY: &[u8] = b"MeijiangData";
const CURRENT_DATA_KEY: &[u8] = b"ZynoteData";

fn utf16_bytes(value: &str) -> Vec<u8> {
    let mut bytes = vec![0xFE, 0xFF];
    for unit in value.encode_utf16() {
        bytes.push((unit >> 8) as u8);
        bytes.push((unit & 0xFF) as u8);
    }
    bytes
}

fn decode_string(object: &Object) -> String {
    match object {
        Object::String(bytes, _) => decode_text_bytes(bytes),
        _ => String::new(),
    }
}

fn decode_text_bytes(bytes: &[u8]) -> String {
    if bytes.len() >= 2 && bytes[0] == 0xFE && bytes[1] == 0xFF {
        let units: Vec<u16> = bytes[2..]
            .chunks(2)
            .filter(|chunk| chunk.len() == 2)
            .map(|chunk| ((chunk[0] as u16) << 8) | chunk[1] as u16)
            .collect();
        String::from_utf16(&units).unwrap_or_default()
    } else {
        String::from_utf8_lossy(bytes).into_owned()
    }
}

fn string_object(value: &str) -> Object {
    Object::String(utf16_bytes(value), StringFormat::Hexadecimal)
}

fn name_starts_with_ours(value: &str) -> bool {
    value.starts_with(LEGACY_PREFIX) || value.starts_with(CURRENT_PREFIX)
}

fn annotation_dict(row: &JsonMap<String, Value>) -> Object {
    let mut dict = lopdf::dictionary! {};
    let id = row.get("id").and_then(|value| value.as_str()).unwrap_or_default();
    let page = row.get("page").and_then(|value| value.as_f64()).unwrap_or_default();
    let rect: Vec<f64> = row
        .get("rect")
        .and_then(|value| value.as_array())
        .map(|items| items.iter().filter_map(|item| item.as_f64()).collect())
        .unwrap_or_default();
    let color: Vec<f64> = row
        .get("color")
        .and_then(|value| value.as_array())
        .map(|items| items.iter().filter_map(|item| item.as_f64()).collect())
        .unwrap_or_default();
    let description = row
        .get("description")
        .and_then(|value| value.as_str())
        .unwrap_or_default();
    let (x, y, width, height) = (rect[0], rect[1], rect[2], rect[3]);
    dict.set("Type", Object::Name(b"Annot".to_vec()));
    dict.set("Subtype", Object::Name(b"Highlight".to_vec()));
    dict.set(
        "Rect",
        Object::Array(vec![
            Object::Real(x as f32),
            Object::Real(y as f32),
            Object::Real((x + width) as f32),
            Object::Real((y + height) as f32),
        ]),
    );
    dict.set(
        "QuadPoints",
        Object::Array(vec![
            Object::Real(x as f32),
            Object::Real((y + height) as f32),
            Object::Real((x + width) as f32),
            Object::Real((y + height) as f32),
            Object::Real(x as f32),
            Object::Real(y as f32),
            Object::Real((x + width) as f32),
            Object::Real(y as f32),
        ]),
    );
    dict.set(
        "C",
        Object::Array(color.into_iter().map(|value| Object::Real(value as f32)).collect()),
    );
    dict.set("CA", Object::Real(0.3));
    dict.set("F", Object::Integer(4));
    dict.set("NM", string_object(&(CURRENT_PREFIX.to_string() + id)));
    dict.set("T", string_object("Zynote"));
    dict.set("Contents", string_object(description));
    let metadata = serde_json::to_string(row).unwrap_or_default();
    dict.set("ZynoteData", string_object(&metadata));
    Object::Dictionary(dict)
}

fn mediabox(document: &LopdfDocument, page_id: ObjectId) -> Option<(f64, f64, f64)> {
    let object = document.objects.get(&page_id)?.clone();
    let dict = document.dereference(&object).ok()?.1.as_dict().ok()?.clone();
    let box_value = dict.get_deref(b"MediaBox", document).ok()?.clone();
    let numbers: Vec<f64> = match box_value {
        Object::Array(items) => items
            .iter()
            .filter_map(|item| match item {
                Object::Integer(value) => Some(*value as f64),
                Object::Real(value) => Some(*value as f64),
                _ => None,
            })
            .collect(),
        _ => return None,
    };
    if numbers.len() != 4 {
        return None;
    }
    let rotation = dict
        .get_deref(b"Rotate", document)
        .ok()
        .and_then(|value| match value {
            Object::Integer(value) => Some(*value as f64),
            _ => None,
        })
        .unwrap_or(0.0);
    Some((numbers[2] - numbers[0], numbers[3] - numbers[1], rotation))
}

fn read_annotations(document: &LopdfDocument, page_id: ObjectId, page_number: u32) -> Result<Vec<Value>, String> {
    let object = document
        .objects
        .get(&page_id)
        .cloned()
        .ok_or("教材页对象缺失，原文件未修改")?;
    let dict = object.as_dict().map_err(|_| "教材页对象无效，原文件未修改")?;
    let annots = dict
        .get_deref(b"Annots", document)
        .ok()
        .cloned()
        .unwrap_or(Object::Null);
    let items = match annots {
        Object::Array(items) => items,
        _ => return Ok(Vec::new()),
    };
    let mut previous = Vec::new();
    for item in &items {
        let (_, resolved) = document
            .dereference(item)
            .map_err(|_| "教材批注无法读取，原文件未修改")?;
        let annotation = resolved.as_dict().map_err(|_| "教材批注无效，原文件未修改")?;
        let nm = annotation
            .get_deref(b"NM", document)
            .map(decode_string)
            .unwrap_or_default();
        if !name_starts_with_ours(&nm) {
            continue;
        }
        let metadata_text = annotation
            .get_deref(CURRENT_DATA_KEY, document)
            .map(decode_string)
            .or_else(|_| annotation.get_deref(LEGACY_DATA_KEY, document).map(decode_string))
            .map_err(|_| "教材划线元数据缺失，原文件未修改")?;
        if metadata_text.is_empty() {
            continue;
        }
        let rect: Vec<f64> = annotation
            .get_deref(b"Rect", document)
            .ok()
            .cloned()
            .ok_or("教材划线坐标无法读取，原文件未修改")
            .and_then(|value| match value {
                Object::Array(items) => Ok(items
                    .iter()
                    .filter_map(|item| match item {
                        Object::Integer(value) => Some(*value as f64),
                        Object::Real(value) => Some(*value as f64),
                        _ => None,
                    })
                    .collect()),
                _ => Err("教材划线坐标无法读取，原文件未修改"),
            })?;
        if rect.len() != 4 {
            return Err("教材划线坐标无法读取，原文件未修改".to_string());
        }
        let mut metadata: JsonMap<String, Value> =
            serde_json::from_str(&metadata_text).map_err(|_| "教材划线元数据无效，原文件未修改")?;
        let (x, y, right, top) = (rect[0], rect[1], rect[2], rect[3]);
        metadata.insert("page".to_string(), json!(page_number));
        metadata.insert(
            "rect".to_string(),
            json!([x, y, right - x, top - y]),
        );
        previous.push(Value::Object(metadata));
    }
    Ok(previous)
}

fn inspect(document: &LopdfDocument) -> Result<Value, String> {
    let pages = document.get_pages();
    let mut sizes = Vec::new();
    let mut previous = Vec::new();
    for (page_number, page_id) in &pages {
        if let Some((width, height, rotation)) = mediabox(document, *page_id) {
            sizes.push(json!([width, height, rotation]));
        }
        previous.extend(read_annotations(document, *page_id, *page_number)?);
    }
    Ok(json!({
        "pages": pages.len(),
        "sizes": sizes,
        "previous": previous
    }))
}

pub fn inspect_pdf(path: &str) -> Result<Value, String> {
    let document = LopdfDocument::load(path).map_err(|error| format!("PDF 无法打开: {error}"))?;
    inspect(&document)
}

pub fn write_annotations(path: &str, output: &str, rows: &Value) -> Result<Value, String> {
    if rows.as_array().is_none() {
        return Err("划线数据无效".to_string());
    }
    let mut document = LopdfDocument::load(path).map_err(|error| format!("PDF 无法打开: {error}"))?;
    let info = inspect(&document)?;
    let pages = document.get_pages();
    let row_list = rows.as_array().unwrap();
    for page_id in pages.values() {
        let existing_annots = match document.get_object_mut(*page_id) {
            Ok(Object::Dictionary(dict)) => dict.get(b"Annots").cloned().unwrap_or(Object::Null),
            _ => Object::Null,
        };
        let kept: Vec<Object> = match existing_annots {
            Object::Array(items) => items
                .into_iter()
                .filter(|item| {
                    let (_, resolved) = document.dereference(item).unwrap_or((None, item));
                    let nm = resolved
                        .as_dict()
                        .ok()
                        .and_then(|annotation| annotation.get_deref(b"NM", &document).ok().map(decode_string))
                        .unwrap_or_default();
                    !name_starts_with_ours(&nm)
                })
                .collect(),
            _ => Vec::new(),
        };
        if let Ok(Object::Dictionary(dict)) = document.get_object_mut(*page_id) {
            dict.set("Annots", Object::Array(kept));
        }
    }
    for row in row_list {
        let dict = row.as_object().ok_or("划线数据无效")?;
        let page = dict.get("page").and_then(|value| value.as_f64()).unwrap_or(0.0);
        let page_id = pages.get(&(page as u32)).ok_or("划线超出教材页面，请重新检查")?;
        let (width, height, _) = mediabox(&document, *page_id).ok_or("教材页面尺寸无法读取，原文件未修改")?;
        let rect: Vec<f64> = dict
            .get("rect")
            .and_then(|value| value.as_array())
            .map(|items| items.iter().filter_map(|item| item.as_f64()).collect())
            .unwrap_or_default();
        if rect.len() != 4 {
            return Err("划线超出教材页面，请重新检查".to_string());
        }
        let (x, y, rect_width, rect_height) = (rect[0], rect[1], rect[2], rect[3]);
        if x < 0.0 || y < 0.0 || rect_width <= 0.0 || rect_height <= 0.0 ||
            x + rect_width > width + 0.1 || y + rect_height > height + 0.1 {
            return Err("划线超出教材页面，请重新检查".to_string());
        }
        let annotation_id = document
            .try_add_object(annotation_dict(dict))
            .map_err(|error| format!("{error}"))?;
        let existing = match document.get_object_mut(*page_id) {
            Ok(Object::Dictionary(page_dict)) => page_dict.get(b"Annots").cloned().unwrap_or(Object::Array(Vec::new())),
            _ => Object::Array(Vec::new()),
        };
        let mut items = match document.dereference(&existing).unwrap_or((None, &existing)).1.clone() {
            Object::Array(items) => items,
            _ => Vec::new(),
        };
        items.push(Object::Reference(annotation_id));
        if let Ok(Object::Dictionary(page_dict)) = document.get_object_mut(*page_id) {
            page_dict.set("Annots", Object::Array(items));
        }
    }
    document.save(output).map_err(|error| format!("PDF 保存失败: {error}"))?;
    let saved = LopdfDocument::load(output).map_err(|error| format!("PDF 回读失败: {error}"))?;
    let verified = inspect(&saved)?;
    if verified.get("pages") != info.get("pages") {
        return Err("保存检查失败，原教材未修改".to_string());
    }
    let verified_previous = verified
        .get("previous")
        .and_then(|value| value.as_array())
        .cloned()
        .unwrap_or_default();
    if verified_previous.len() != row_list.len() {
        return Err("保存检查失败，原教材未修改".to_string());
    }
    for row in row_list {
        let dict = row.as_object().ok_or("划线数据无效")?;
        let id = dict.get("id").and_then(|value| value.as_str()).unwrap_or_default();
        let page = dict.get("page").and_then(|value| value.as_f64()).unwrap_or_default();
        let rect: Vec<f64> = dict
            .get("rect")
            .and_then(|value| value.as_array())
            .map(|items| items.iter().filter_map(|item| item.as_f64()).collect())
            .unwrap_or_default();
        let matched = verified_previous.iter().any(|value| {
            let item = match value.as_object() {
                Some(item) => item,
                None => return false,
            };
            item.get("id").and_then(|value| value.as_str()) == Some(id)
                && item.get("page").and_then(|value| value.as_f64()) == Some(page)
                && item
                    .get("rect")
                    .and_then(|value| value.as_array())
                    .map(|numbers| {
                        numbers.len() == rect.len()
                            && numbers
                                .iter()
                                .zip(rect.iter())
                                .all(|(a, b)| (a.as_f64().unwrap_or_default() - b).abs() < 0.00001)
                    })
                    .unwrap_or(false)
        });
        if !matched {
            return Err(format!("划线 {} 保存检查失败，原教材未修改", id));
        }
    }
    let previous_count = info
        .get("previous")
        .and_then(|value| value.as_array())
        .map(|items| items.len())
        .unwrap_or(0);
    Ok(json!({"pages": verified.get("pages").cloned().unwrap_or(json!(0)), "count": previous_count}))
}

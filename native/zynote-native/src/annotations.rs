use lopdf::{dictionary, Document as LopdfDocument, Object, ObjectId, StringFormat};
use serde_json::{json, Map as JsonMap, Value};
use std::fmt::Write;

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

// The appearance stream makes the margin note visible in ordinary PDF readers as well as Zynote.
// UniGB-UCS2-H maps Unicode using the standard Adobe GB1 CJK font collection.
fn chapter_appearance(document: &mut LopdfDocument, text: &str, width: f64, height: f64) -> Result<ObjectId, String> {
    let cid = document.add_object(dictionary! {
        "Type" => "Font", "Subtype" => "CIDFontType0", "BaseFont" => "STSong-Light",
        "CIDSystemInfo" => dictionary! { "Registry" => Object::string_literal("Adobe"),
            "Ordering" => Object::string_literal("GB1"), "Supplement" => 4 },
        "DW" => 1000,
    });
    let font = document.add_object(dictionary! {
        "Type" => "Font", "Subtype" => "Type0", "BaseFont" => "STSong-Light",
        "Encoding" => "UniGB-UCS2-H", "DescendantFonts" => vec![Object::Reference(cid)],
    });
    // Try a normal reading size first and shrink only if the actual summary cannot fit.
    let mut size = 9.0_f64;
    let lines: Vec<String> = loop {
        let columns = ((width-4.0)/size).floor().max(1.0) as usize;
        let lines: Vec<String> = text.lines().flat_map(|line| {
            line.chars().collect::<Vec<_>>().chunks(columns).map(|chunk| chunk.iter().collect()).collect::<Vec<String>>()
        }).collect();
        if (lines.len() as f64)*size*1.26 <= height-3.0 || size<=3.0 {break lines;}
        size -= 0.5;
    };
    let mut content = format!("q BT /F0 {size:.2} Tf 0.12 0.18 0.22 rg {leading:.3} TL 2 {y:.3} Td\n",
        leading=size*1.26,y=height-size-1.0);
    for line in lines {
        let mut hex = String::new();
        for unit in line.encode_utf16() {write!(hex,"{unit:04X}").map_err(|e| e.to_string())?;}
        writeln!(content,"<{hex}> Tj T*").map_err(|e| e.to_string())?;
    }
    content.push_str("ET Q\n");
    let appearance = lopdf::Stream::new(dictionary! {
        "Type" => "XObject", "Subtype" => "Form",
        "BBox" => vec![0.into(), 0.into(), Object::Real(width as f32), Object::Real(height as f32)],
        "Resources" => dictionary! {"Font" => dictionary! {"F0" => Object::Reference(font)}},
    }, content.into_bytes());
    document.try_add_object(appearance).map_err(|error|error.to_string())
}

fn annotation_dict(document: &mut LopdfDocument, row: &JsonMap<String, Value>) -> Result<Object, String> {
    let mut dict = lopdf::dictionary! {};
    let id = row.get("id").and_then(|value| value.as_str()).unwrap_or_default();
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
    if row.contains_key("chapter") {
        let appearance = chapter_appearance(document, description, width, height)?;
        dict.set("Subtype", Object::Name(b"FreeText".to_vec()));
        dict.set("DA", Object::string_literal("/Helv 9 Tf 0 g"));
        dict.set("Border", vec![0.into(), 0.into(), 0.into()]);
        dict.set("AP", dictionary! {"N" => Object::Reference(appearance)});
    } else if let Some(ink) = row.get("ink") {
        let points: Vec<[f64; 2]> = serde_json::from_value(ink.clone()).map_err(|_| "笔迹坐标无效")?;
        if !(2..=4096).contains(&points.len()) || color.len() != 3 {
            return Err("笔迹数据无效".to_string());
        }
        let mut coordinates = Vec::with_capacity(points.len() * 2);
        let mut drawing = format!("q\n{:.4} {:.4} {:.4} RG\n1.6 w 1 J 1 j\n", color[0], color[1], color[2]);
        for (index, [px, py]) in points.into_iter().enumerate() {
            if px < x - 0.02 || py < y - 0.02 || px > x + width + 0.02 || py > y + height + 0.02 {
                return Err("笔迹超出选区".to_string());
            }
            coordinates.extend([Object::Real(px as f32), Object::Real(py as f32)]);
            writeln!(drawing, "{:.4} {:.4} {}", px - x, py - y, if index == 0 { "m" } else { "l" })
                .map_err(|error| error.to_string())?;
        }
        drawing.push_str("S\nQ\n");
        let appearance = lopdf::Stream::new(lopdf::dictionary! {
            "Type" => "XObject", "Subtype" => "Form",
            "BBox" => vec![0.into(), 0.into(), Object::Real(width as f32), Object::Real(height as f32)],
            "Resources" => lopdf::dictionary! {},
        }, drawing.into_bytes());
        let reference = document.try_add_object(appearance).map_err(|error| error.to_string())?;
        dict.set("AP", lopdf::dictionary! {"N" => Object::Reference(reference)});
        dict.set("Subtype", Object::Name(b"Ink".to_vec()));
        dict.set("InkList", Object::Array(vec![Object::Array(coordinates)]));
        dict.set("BS", lopdf::dictionary! {"W" => Object::Real(1.6), "S" => "S"});
    } else {
        dict.set("CA", Object::Real(0.3));
        dict.set("QuadPoints", Object::Array(vec![
            Object::Real(x as f32),
            Object::Real((y + height) as f32),
            Object::Real((x + width) as f32),
            Object::Real((y + height) as f32),
            Object::Real(x as f32),
            Object::Real(y as f32),
            Object::Real((x + width) as f32),
            Object::Real(y as f32),
        ]));
    }
    dict.set(
        "C",
        Object::Array(color.into_iter().map(|value| Object::Real(value as f32)).collect()),
    );
    dict.set("F", Object::Integer(4));
    dict.set("NM", string_object(&(CURRENT_PREFIX.to_string() + id)));
    dict.set("T", string_object("Zynote"));
    dict.set("Contents", string_object(description));
    let metadata = serde_json::to_string(row).unwrap_or_default();
    dict.set("ZynoteData", string_object(&metadata));
    Ok(Object::Dictionary(dict))
}

fn inherited<'a>(document: &'a LopdfDocument, mut page: ObjectId, key: &[u8]) -> Option<&'a Object> {
    let mut visited = std::collections::HashSet::new();
    while visited.insert(page) {
        let dict = document.get_dictionary(page).ok()?;
        if let Ok(value) = dict.get_deref(key, document) {
            return Some(value);
        }
        page = dict.get(b"Parent").ok()?.as_reference().ok()?;
    }
    None
}

fn mediabox(document: &LopdfDocument, page: ObjectId) -> Option<[f64; 4]> {
    let values = inherited(document, page, b"MediaBox")?.as_array().ok()?;
    let [left, bottom, right, top] = values.as_slice() else { return None };
    Some([left.as_float().ok()? as f64, bottom.as_float().ok()? as f64,
          right.as_float().ok()? as f64, top.as_float().ok()? as f64])
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
        if let Some([left, bottom, right, top]) = mediabox(document, *page_id) {
            let rotation = inherited(document, *page_id, b"Rotate").and_then(|v| v.as_i64().ok()).unwrap_or(0);
            sizes.push(json!([right-left, top-bottom, rotation]));
        } else {
            sizes.push(Value::Null);
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
        let kept: Vec<Object> = match document.dereference(&existing_annots).map_err(|_| "教材页批注无法读取")?.1.clone() {
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
        let [left, bottom, right, top] = mediabox(&document, *page_id).ok_or("教材页面尺寸无法读取，原文件未修改")?;
        let rect: Vec<f64> = dict
            .get("rect")
            .and_then(|value| value.as_array())
            .map(|items| items.iter().filter_map(|item| item.as_f64()).collect())
            .unwrap_or_default();
        if rect.len() != 4 {
            return Err("划线超出教材页面，请重新检查".to_string());
        }
        let (x, y, rect_width, rect_height) = (rect[0], rect[1], rect[2], rect[3]);
        if x < left - 0.01 || y < bottom - 0.01 || rect_width <= 0.0 || rect_height <= 0.0 ||
            x + rect_width > right + 0.1 || y + rect_height > top + 0.1 {
            return Err("划线超出教材页面，请重新检查".to_string());
        }
        let annotation = annotation_dict(&mut document, dict)?;
        let annotation_id = document
            .try_add_object(annotation)
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
                && item.get("comments") == dict.get("comments") && item.get("chapter") == dict.get("chapter")
                && item
                    .get("rect")
                    .and_then(|value| value.as_array())
                    .map(|numbers| {
                        numbers.len() == rect.len()
                            && numbers
                                .iter()
                                .zip(rect.iter())
                                .all(|(a, b)| (a.as_f64().unwrap_or_default() - b).abs() < 0.001)
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

#[cfg(test)]
mod tests {
    use super::*;
    use lopdf::dictionary;
    use serde_json::json;

    fn utf16_round_trip(value: &str) {
        assert_eq!(decode_text_bytes(&utf16_bytes(value)), value);
    }

    #[test]
    fn utf16_bytes_use_bom_and_round_trip() {
        assert_eq!(utf16_bytes("A"), vec![0xFE, 0xFF, 0x00, 0x41]);
        for value in ["", "摘云", "emoji 🎧", "line\nbreak", "a\u{0}b"] {
            utf16_round_trip(value);
        }
    }

    #[test]
    fn utf8_without_bom_decodes() {
        assert_eq!(decode_text_bytes("旧批注".as_bytes()), "旧批注");
    }

    #[test]
    fn prefixes_are_recognized() {
        assert!(name_starts_with_ours("zynote:abc"));
        assert!(name_starts_with_ours("meijiang:legacy"));
        assert!(!name_starts_with_ours("other"));
        assert!(!name_starts_with_ours(""));
    }

    #[test]
    fn annotation_dict_sets_current_keys() {
        let row = json!({
            "id": "abc",
            "page": 1,
            "rect": [1.0, 2.0, 3.0, 4.0],
            "color": [0.7, 0.5, 0.2],
            "description": "批注"
        });
        let Object::Dictionary(dict) = annotation_dict(&mut LopdfDocument::new(), row.as_object().unwrap()).unwrap() else {
            panic!("expected dictionary");
        };
        assert_eq!(dict.get(b"Subtype").unwrap(), &Object::Name(b"Highlight".to_vec()));
        assert_eq!(dict.get(b"NM").unwrap(), &string_object("zynote:abc"));
        assert_eq!(dict.get(b"T").unwrap(), &string_object("Zynote"));
        assert_eq!(dict.get(b"Contents").unwrap(), &string_object("批注"));
        assert!(dict.get(CURRENT_DATA_KEY).is_ok());
        assert!(dict.get(LEGACY_DATA_KEY).is_err());
    }

    #[test]
    fn annotations_round_trip_through_pdf() {
        let directory = std::env::temp_dir().join(format!("zynote-annotations-{}", std::process::id()));
        std::fs::create_dir_all(&directory).unwrap();
        let source = directory.join("source.pdf");
        let output = directory.join("output.pdf");
        let mut document = LopdfDocument::with_version("1.5");
        let pages_id = document.new_object_id();
        let page_id = document.new_object_id();
        document.objects.insert(
            page_id,
            Object::Dictionary(dictionary! {
                "Type" => "Page",
                "Parent" => pages_id,
                "MediaBox" => vec![0.into(), 0.into(), 612.into(), 792.into()],
            }),
        );
        document.objects.insert(
            pages_id,
            Object::Dictionary(dictionary! {
                "Type" => "Pages",
                "Kids" => vec![page_id.into()],
                "Count" => 1,
            }),
        );
        let catalog_id = document.add_object(dictionary! {
            "Type" => "Catalog",
            "Pages" => pages_id,
        });
        document.trailer.set("Root", catalog_id);
        document.save(&source).unwrap();

        let row = json!({
            "id": "abc", "page": 1, "rect": [10.0, 20.0, 30.0, 40.0],
            "color": [0.7, 0.5, 0.2], "description": "测试批注",
            "createdAt": "2026-10-23T00:00:00Z"
        });
        let stroke = json!({
            "id": "pen", "page": 1, "rect": [20.25, 32.5, 40.75, 50.25],
            "ink": [[21, 33], [54, 79], [30, 67]],
            "color": [0.7, 0.5, 0.2], "description": "手动笔迹"
        });
        let chapter = json!({
            "id":"summary","page":1,"rect":[420.0,600.0,150.0,60.0],"color":[0.2,0.2,0.2],
            "description":"理解定义和例题条件。\n第2页 第6行",
            "chapter":{"title":"第一章","text":"理解定义和例题条件。","refs":[{"page":2,"line":6}]}
        });
        let report = write_annotations(
            source.to_str().unwrap(),
            output.to_str().unwrap(),
            &json!([row, stroke, chapter]),
        )
        .unwrap();
        assert_eq!(report["pages"], json!(1));
        assert_eq!(report["count"], json!(0));

        let inspected = inspect_pdf(output.to_str().unwrap()).unwrap();
        let previous = inspected["previous"].as_array().unwrap();
        assert_eq!(previous.len(), 3);
        assert_eq!(previous[2]["chapter"], chapter["chapter"]);
        let item = previous[0].as_object().unwrap();
        assert_eq!(item["id"], json!("abc"));
        assert_eq!(item["rect"], json!([10.0, 20.0, 30.0, 40.0]));
        assert_eq!(item["description"], json!("测试批注"));
        assert_eq!(previous[1]["ink"], stroke["ink"]);
        let saved = LopdfDocument::load(&output).unwrap();
        let ink = saved.objects.values().filter_map(|obj| obj.as_dict().ok())
            .find(|dict| dict.get(b"Subtype").and_then(Object::as_name).ok() == Some(b"Ink")).unwrap();
        let free_text=saved.objects.values().filter_map(|obj|obj.as_dict().ok())
            .find(|d|d.get(b"Subtype").and_then(Object::as_name).ok()==Some(b"FreeText")).unwrap();
        assert_eq!(decode_string(free_text.get(b"Contents").unwrap()),chapter["description"].as_str().unwrap());
        let ap=free_text.get(b"AP").unwrap().as_dict().unwrap().get_deref(b"N",&saved).unwrap().as_stream().unwrap();
        assert!(std::str::from_utf8(&ap.content).unwrap().contains("7406")); // 理
        let paths = ink.get(b"InkList").unwrap().as_array().unwrap();
        assert_eq!(paths[0].as_array().unwrap().len(), 6);
        assert!(ink.get(b"AP").is_ok());
        assert!(ink.get(b"QuadPoints").is_err());
        std::fs::remove_dir_all(&directory).ok();
    }
}

use pdfium_render::prelude::*;
use serde_json::{json, Value};

type Bounds = [f32; 4]; // left, bottom, right, top in PDF user space

#[derive(Default)]
struct Line {
    text: String,
    glyphs: Vec<Option<Bounds>>,
    bounds: Option<Bounds>,
}

impl Line {
    fn finish(&mut self, output: &mut Vec<Value>) {
        let line = std::mem::take(self);
        if let Some([left, bottom, right, top]) = line.bounds {
            output.push(json!({
                "text": line.text, "glyphs": line.glyphs, "x": left, "y": bottom,
                "width": right - left, "height": top - bottom
            }));
        }
    }

    fn push(&mut self, ch: char, rect: Bounds) {
        self.text.push(ch);
        self.glyphs.push(Some(rect));
        self.bounds = Some(match self.bounds {
            Some(b) => [
                b[0].min(rect[0]), b[1].min(rect[1]),
                b[2].max(rect[2]), b[3].max(rect[3]),
            ],
            None => rect,
        });
    }
}

#[derive(Clone, Copy)]
struct Placement {
    origin: (f32, f32),
    vertical: bool,
    size: f32,
    rect: Bounds,
}

impl Placement {
    fn from_glyph(glyph: &PdfPageTextChar, view: Bounds) -> Option<Self> {
        let b = glyph.tight_bounds().ok()?;
        let rect = [
            b.left().value.max(view[0]), b.bottom().value.max(view[1]),
            b.right().value.min(view[2]), b.top().value.min(view[3]),
        ];
        let (x, y) = glyph.origin().ok()?;
        let angle = glyph.angle_radians().ok()?;
        let size = glyph.scaled_font_size().value.abs().max(1.0);
        // Axis-aligned highlight quads cannot accurately follow diagonal/curved text.
        if rect[0] >= rect[2] || rect[1] >= rect[3] || angle.sin().abs().min(angle.cos().abs()) > 0.1 {
            return None;
        }
        Some(Self { origin: (x.value, y.value), vertical: angle.sin().abs() > 0.5, size, rect })
    }

    fn same_run(self, other: Self) -> bool {
        let em = self.size.max(other.size);
        let baseline = if self.vertical {
            self.origin.0 - other.origin.0
        } else {
            self.origin.1 - other.origin.1
        };
        let gap_x = (self.rect[0] - other.rect[2]).max(other.rect[0] - self.rect[2]);
        let gap_y = (self.rect[1] - other.rect[3]).max(other.rect[1] - self.rect[3]);
        // Some PDFs place two columns in a single text stream without a line break.
        self.vertical == other.vertical && baseline.abs() <= 0.4 * em && gap_x.max(gap_y) <= 1.1 * em
    }
}

pub(crate) fn lines(text: &PdfPageText, view: Bounds) -> Result<Vec<Value>, String> {
    if text.len() > 50_000 {
        return Err("该页文字过密，无法安全读取字形坐标".into());
    }
    let mut output = Vec::new();
    let mut line = Line::default();
    let mut previous: Option<Placement> = None;
    for glyph in text.chars().iter() {
        let value = glyph.unicode_char();
        if let Some(ch) = value.filter(|c| c.is_whitespace() && !c.is_control()) {
            if line.bounds.is_some() {
                line.text.push(ch);
                line.glyphs.push(None);
            }
            continue;
        }
        let placement = value.filter(|c| !c.is_control())
            .and_then(|ch| Placement::from_glyph(&glyph, view).map(|pos| (ch, pos)));
        let Some((ch, current)) = placement else {
            line.finish(&mut output);
            previous = None;
            continue;
        };
        if previous.is_some_and(|last| !last.same_run(current)) {
            line.finish(&mut output);
        }
        line.push(ch, current.rect);
        previous = Some(current);
    }
    line.finish(&mut output);
    Ok(output)
}

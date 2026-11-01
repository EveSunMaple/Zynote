use pdfium_render::prelude::*;

// PDFium reports child objects in their form's coordinate space.
pub(crate) fn has_body_image(page: &PdfPage, view_box: [f32; 4]) -> bool {
    let [left, bottom, right, top] = view_box;
    if right <= left || top <= bottom {
        return false;
    }
    fn sizable_image(object: &PdfPageObject, parent: PdfMatrix, view: [f32; 4], depth: u8) -> bool {
        match object {
            PdfPageObject::Image(image) => {
                let Ok(bounds) = image.bounds() else { return false };
                let rect = bounds.transform(parent).to_rect();
                let [left, bottom, right, top] = view;
                let width = (rect.right.value.min(right) - rect.left.value.max(left)).max(0.0);
                let height = (rect.top.value.min(top) - rect.bottom.value.max(bottom)).max(0.0);
                width * height >= (right - left) * (top - bottom) * 0.25
            }
            PdfPageObject::XObjectForm(form) if depth < 12 => {
                let Ok(matrix) = form.matrix() else { return false };
                let transform = matrix.multiply(parent);
                form.iter().any(|child| sizable_image(&child, transform, view, depth + 1))
            }
            _ => false,
        }
    }
    page.objects().iter().any(|object| sizable_image(&object, PdfMatrix::IDENTITY, view_box, 0))
}

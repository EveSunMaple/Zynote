use rustypipe::client::{RustyPipe, RustyPipeBuilder};
use rustypipe::model::{AudioCodec, AudioFormat, VideoCodec, VideoFormat};
use serde_json::{json, Value};

pub struct YoutubeService {
    runtime: tokio::runtime::Runtime,
    pipe: RustyPipe,
    authenticated: std::sync::atomic::AtomicBool,
}

impl YoutubeService {
    pub fn new(cache_dir: Option<&str>) -> Result<Self, String> {
        let mut builder = RustyPipeBuilder::new().visitor_data(generated_visitor_data());
        if let Some(directory) = cache_dir {
            std::fs::create_dir_all(directory).map_err(|error| format!("YouTube 缓存目录不可用：{error}"))?;
            builder = builder.storage_dir(directory);
        }
        let pipe = builder.build().map_err(|error| format!("YouTube 客户端初始化失败：{error}"))?;
        let runtime = tokio::runtime::Builder::new_multi_thread()
            .enable_all()
            .build()
            .map_err(|error| format!("YouTube 运行时初始化失败：{error}"))?;
        Ok(Self { runtime, pipe, authenticated: std::sync::atomic::AtomicBool::new(false) })
    }

    pub fn auth(&self, cookie: &str) -> Result<Value, String> {
        if cookie.is_empty() {
            self.authenticated.store(false, std::sync::atomic::Ordering::SeqCst);
            return Ok(json!({"authenticated": false}));
        }
        if cookie.len() > 20000 {
            return Err("YouTube 登录 Cookie 无效".to_string());
        }
        match std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            self.runtime.block_on(self.pipe.user_auth_set_cookie(cookie))
        })) {
            Ok(Ok(())) => {
                self.authenticated.store(true, std::sync::atomic::Ordering::SeqCst);
                Ok(json!({"authenticated": true}))
            }
            Ok(Err(error)) => Err(format!("YouTube 登录状态不可用：{error}")),
            Err(_) => Err("YouTube 登录状态不可用：内部错误".to_string()),
        }
    }

    fn query(&self) -> rustypipe::client::RustyPipeQuery {
        let query = self.pipe.query();
        if self.authenticated.load(std::sync::atomic::Ordering::SeqCst) {
            query.authenticated()
        } else {
            query
        }
    }

    pub fn video(&self, id: &str) -> Result<Value, String> {
        let player = match std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            self.runtime.block_on(self.query().player(id))
        })) {
            Ok(Ok(player)) => player,
            Ok(Err(error)) => return Err(format!("YouTube 目录读取失败：{error}")),
            Err(_) => return Err("YouTube 目录读取失败：内部错误".to_string()),
        };
        if player.details.id != id {
            return Err("YouTube 目录与所选视频身份不一致".to_string());
        }
        if player.details.is_live || player.details.duration == 0 {
            return Err("此 YouTube 视频未确认可下载，或仍为直播".to_string());
        }
        let mut streams = Vec::new();
        let mut formats = Vec::new();
        let mut heights = std::collections::BTreeSet::new();
        let mut seen = std::collections::HashSet::new();
        for stream in &player.video_streams {
            if stream.drm_track_type.is_some() || !stream.drm_systems.is_empty() {
                continue;
            }
            if !seen.insert(stream.itag) {
                continue;
            }
            let extension = video_extension(stream.format);
            let codec = video_codec(stream.codec);
            heights.insert(stream.height);
            formats.push(json!({
                "id": stream.itag.to_string(), "height": stream.height,
                "extension": extension, "video_codec": codec, "audio_codec": codec
            }));
            streams.push(json!({
                "id": stream.itag.to_string(), "kind": "combined", "urls": [stream.url],
                "height": stream.height, "codec": codec, "bandwidth": stream.bitrate, "extension": extension
            }));
        }
        for stream in &player.video_only_streams {
            if stream.drm_track_type.is_some() || !stream.drm_systems.is_empty() {
                continue;
            }
            if !seen.insert(stream.itag) {
                continue;
            }
            let extension = video_extension(stream.format);
            let codec = video_codec(stream.codec);
            heights.insert(stream.height);
            formats.push(json!({
                "id": stream.itag.to_string(), "height": stream.height,
                "extension": extension, "video_codec": codec, "audio_codec": "none"
            }));
            streams.push(json!({
                "id": stream.itag.to_string(), "kind": "video", "urls": [stream.url],
                "height": stream.height, "codec": codec, "bandwidth": stream.bitrate, "extension": extension
            }));
        }
        for stream in &player.audio_streams {
            if stream.drm_track_type.is_some() || !stream.drm_systems.is_empty() {
                continue;
            }
            if !seen.insert(stream.itag) {
                continue;
            }
            let extension = audio_extension(stream.format);
            let codec = audio_codec(stream.codec);
            formats.push(json!({
                "id": stream.itag.to_string(), "height": Value::Null,
                "extension": extension, "video_codec": "none", "audio_codec": codec
            }));
            streams.push(json!({
                "id": stream.itag.to_string(), "kind": "audio", "urls": [stream.url],
                "height": 0, "codec": codec, "bandwidth": stream.bitrate, "extension": extension
            }));
        }
        let subtitles = player
            .subtitles
            .iter()
            .map(|subtitle| {
                let language = if subtitle.auto_generated {
                    format!("ai-{}", subtitle.lang)
                } else {
                    subtitle.lang.clone()
                };
                json!({
                    "language": language,
                    "url": subtitle_format_url(&subtitle.url),
                    "automatic": subtitle.auto_generated,
                    "format": "youtube-json3"
                })
            })
            .collect::<Vec<Value>>();
        let languages = subtitles
            .iter()
            .filter_map(|track| track.get("language").and_then(|value| value.as_str()))
            .map(|value| value.to_string())
            .collect::<Vec<String>>();
        let thumbnail = player
            .details
            .thumbnail
            .iter()
            .max_by_key(|image| image.width)
            .map(|image| image.url.clone())
            .unwrap_or_default();
        let title = player.details.name.clone().unwrap_or_else(|| id.to_string());
        let duration = f64::from(player.details.duration);
        let source = json!({
            "platform": "youtube",
            "sourceId": id,
            "identity": format!("youtube:{id}"),
            "streams": streams,
            "subtitleTracks": subtitles,
            "description": player.details.description.clone().unwrap_or_default(),
            "downloadHeaders": {"Referer": "https://www.youtube.com/", "User-Agent": "Mozilla/5.0"},
            "thumbnail": thumbnail,
            "catalog": {
                "url": format!("https://www.youtube.com/watch?v={id}"),
                "title": truncate(&title, 240),
                "playlist": false,
                "duration": duration,
                "heights": heights.into_iter().collect::<Vec<u32>>(),
                "subtitles": languages,
                "formats": formats
            }
        });
        Ok(source)
    }

    pub fn playlist(&self, _id: &str) -> Result<Value, String> {
        Err("YouTube 播放列表暂未接入原生执行器，请逐条选择视频".to_string())
    }
}

fn truncate(value: &str, limit: usize) -> String {
    value.chars().take(limit).collect()
}

fn video_codec(codec: VideoCodec) -> &'static str {
    match codec {
        VideoCodec::Avc1 => "avc1",
        VideoCodec::Vp9 => "vp9",
        VideoCodec::Av01 => "av01",
        VideoCodec::Mp4v => "mp4v",
        _ => "",
    }
}

fn audio_codec(codec: AudioCodec) -> &'static str {
    match codec {
        AudioCodec::Mp4a => "mp4a",
        AudioCodec::Opus => "opus",
        AudioCodec::Ac3 => "ac3",
        AudioCodec::Ec3 => "ec3",
        _ => "",
    }
}

fn video_extension(format: VideoFormat) -> &'static str {
    match format {
        VideoFormat::Mp4 => "mp4",
        VideoFormat::Webm => "webm",
        VideoFormat::ThreeGp => "3gp",
        _ => "mp4",
    }
}

fn audio_extension(format: AudioFormat) -> &'static str {
    match format {
        AudioFormat::M4a => "m4a",
        AudioFormat::Webm => "webm",
        _ => "m4a",
    }
}

fn subtitle_format_url(url: &str) -> String {
    if url.contains("fmt=") {
        return url.to_string();
    }
    if url.contains('?') {
        format!("{url}&fmt=json3")
    } else {
        format!("{url}?fmt=json3")
    }
}

fn generated_visitor_data() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|value| value.as_nanos() as u64)
        .unwrap_or(0);
    let alphabet = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-";
    let mut state = nanos ^ (u64::from(std::process::id())).wrapping_mul(0x9E37_79B9_7F4A_7C15);
    let mut identifier = String::new();
    for _ in 0..11 {
        state = state
            .wrapping_mul(6_364_136_223_846_793_005)
            .wrapping_add(1_442_695_040_888_963_407);
        identifier.push(alphabet[((state >> 33) as usize) % alphabet.len()] as char);
    }
    let mut bytes = vec![0x0a, 0x0b];
    bytes.extend_from_slice(identifier.as_bytes());
    bytes.push(0x28);
    let mut timestamp = nanos / 1_000_000;
    loop {
        let mut byte = (timestamp & 0x7f) as u8;
        timestamp >>= 7;
        if timestamp != 0 {
            byte |= 0x80;
        }
        bytes.push(byte);
        if timestamp == 0 {
            break;
        }
    }
    base64_url(&bytes)
}

fn base64_url(data: &[u8]) -> String {
    const ALPHABET: &[u8] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
    let mut output = String::new();
    for chunk in data.chunks(3) {
        let first = chunk[0] as u32;
        let second = chunk.get(1).copied().unwrap_or(0) as u32;
        let third = chunk.get(2).copied().unwrap_or(0) as u32;
        let value = (first << 16) | (second << 8) | third;
        output.push(ALPHABET[((value >> 18) & 63) as usize] as char);
        output.push(ALPHABET[((value >> 12) & 63) as usize] as char);
        if chunk.len() > 1 {
            output.push(ALPHABET[((value >> 6) & 63) as usize] as char);
        }
        if chunk.len() > 2 {
            output.push(ALPHABET[(value & 63) as usize] as char);
        }
    }
    output
}

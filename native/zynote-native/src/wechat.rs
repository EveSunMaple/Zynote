use serde_json::{json, Value};
use std::sync::Mutex;
use std::time::Instant;

pub const CAPABILITIES: &str = r#"{"directory":true,"receive":true,"draft":true,"send":false,"images":false,"automation":false,"receive_mode":"selected_peer_sequence","background_read":true}"#;

struct Probe {
    reader: platform::Reader,
    expires: Instant,
}

#[derive(Default)]
struct State {
    probes: std::collections::HashMap<String, Probe>,
    readers: std::collections::HashMap<String, platform::Reader>,
}

pub struct WechatService {
    state: Mutex<State>,
}

impl Default for WechatService {
    fn default() -> Self {
        Self::new()
    }
}

impl WechatService {
    pub fn new() -> Self {
        Self {
            state: Mutex::new(State::default()),
        }
    }

    fn expire(state: &mut State) {
        let now = Instant::now();
        state.probes.retain(|_, probe| probe.expires > now);
    }

    fn reader<'a>(
        state: &'a mut State,
        session: &Value,
    ) -> Result<&'a mut platform::Reader, String> {
        let reader_id = session
            .get("reader_id")
            .and_then(Value::as_str)
            .ok_or("本机微信绑定已失效，请重新核验")?;
        state
            .readers
            .get_mut(reader_id)
            .ok_or_else(|| "本机微信绑定已失效，请重新选择进程核验".to_string())
    }

    fn session_account(params: &Value) -> Result<&str, String> {
        params
            .get("account")
            .and_then(Value::as_str)
            .filter(|value| !value.is_empty() && value.chars().count() <= 200)
            .ok_or_else(|| "本机微信账号参数无效".to_string())
    }

    pub fn dispatch(&self, method: &str, params: &Value) -> Result<Value, String> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| "本机微信读取状态不可用".to_string())?;
        Self::expire(&mut state);
        match method {
            "wechat.processes" => platform::processes(),
            "wechat.probe" => self.probe(&mut state, params),
            "wechat.confirm" => self.confirm(&mut state, params),
            "wechat.verify" => {
                let account = Self::session_account(params)?;
                let session = params.get("session").ok_or("本机微信会话缺失")?;
                Self::reader(&mut state, session)?.verify(account)?;
                Ok(json!({"verified": true}))
            }
            "wechat.directory" => {
                let account = Self::session_account(params)?;
                let session = params.get("session").ok_or("本机微信会话缺失")?;
                let reader = Self::reader(&mut state, session)?;
                reader.verify(account)?;
                reader.directory()
            }
            "wechat.receive" => {
                let account = Self::session_account(params)?;
                let session = params.get("session").ok_or("本机微信会话缺失")?;
                let peer = params
                    .get("peer")
                    .and_then(Value::as_str)
                    .ok_or("本机微信会话参数无效")?;
                let reader = Self::reader(&mut state, session)?;
                reader.verify(account)?;
                reader.receive(peer, session)
            }
            "wechat.invalidate" => {
                if let Some(reader_id) = params.get("reader_id").and_then(Value::as_str) {
                    state.readers.remove(reader_id);
                }
                state.probes.retain(|_, probe| {
                    params
                        .get("reader_id")
                        .and_then(Value::as_str)
                        .map(|id| probe.reader.reader_id() != id)
                        .unwrap_or(false)
                });
                if params.get("reader_id").is_none() {
                    state.readers.clear();
                    state.probes.clear();
                }
                Ok(json!({"invalidated": true}))
            }
            other => Err(format!("本机微信操作无效: {other}")),
        }
    }

    fn probe(&self, state: &mut State, params: &Value) -> Result<Value, String> {
        if state.probes.len() >= 4 {
            return Err("待确认微信读取绑定已达到上限".to_string());
        }
        let pid = params
            .get("pid")
            .and_then(Value::as_u64)
            .and_then(|value| u32::try_from(value).ok())
            .filter(|value| *value > 0)
            .ok_or("微信进程编号无效")?;
        let created = params.get("created").and_then(Value::as_f64);
        let reader = platform::Reader::open(pid, created)?;
        let token = random_id();
        let account = reader.account().to_string();
        state.probes.insert(
            token.clone(),
            Probe {
                reader,
                expires: Instant::now() + std::time::Duration::from_secs(120),
            },
        );
        Ok(json!({
            "probe_id": token,
            "account": account,
            "capabilities": serde_json::from_str::<Value>(CAPABILITIES).unwrap_or(Value::Null),
            "state": "local_read_ready",
            "network_online": Value::Null,
            "expires_in": 120
        }))
    }

    fn confirm(&self, state: &mut State, params: &Value) -> Result<Value, String> {
        let probe_id = params
            .get("probe_id")
            .and_then(Value::as_str)
            .ok_or("微信核验编号无效")?;
        let account = Self::session_account(params)?;
        let mut probe = state
            .probes
            .remove(probe_id)
            .ok_or("本机微信读取绑定已失效")?;
        if probe.expires <= Instant::now() || probe.reader.account() != account {
            return Err("本机微信读取绑定已失效".to_string());
        }
        probe.reader.verify(account)?;
        let reader_id = probe.reader.reader_id().to_string();
        state.readers.insert(reader_id.clone(), probe.reader);
        Ok(json!({
            "account": account,
            "session": {"reader_id": reader_id, "cursors": {}},
            "capabilities": serde_json::from_str::<Value>(CAPABILITIES).unwrap_or(Value::Null)
        }))
    }

    pub fn shutdown(&self) {
        if let Ok(mut state) = self.state.lock() {
            state.probes.clear();
            state.readers.clear();
        }
    }
}

fn random_id() -> String {
    platform::random_bytes(16)
        .iter()
        .map(|value| format!("{value:02x}"))
        .collect()
}

pub fn key_hex(key: &[u8]) -> String {
    key.iter().map(|value| format!("{value:02x}")).collect()
}

pub fn parse_cursor(value: Option<&Value>, reader_id: &str) -> Result<Value, String> {
    let session = value.ok_or("本机微信会话缺失")?;
    if session.get("reader_id").and_then(Value::as_str) != Some(reader_id) {
        return Err("本机微信绑定已失效，请重新核验".to_string());
    }
    Ok(session.get("cursors").cloned().unwrap_or_else(|| json!({})))
}

#[cfg(windows)]
mod platform {
    use super::key_hex;
    use hmac::{Hmac, Mac};
    use md5::Md5;
    use pbkdf2::pbkdf2_hmac;
    use rusqlite::types::ValueRef;
    use rusqlite::{Connection, OpenFlags};
    use serde_json::{json, Map, Value};
    use sha2::{Digest, Sha256, Sha512};
    use std::collections::{HashMap, HashSet};
    use std::ffi::OsStr;
    use std::fs::{self, File};
    use std::io::Read;
    use std::os::windows::ffi::OsStrExt;
    use std::path::{Path, PathBuf};
    use std::ptr;
    use std::time::{Duration, Instant};

    type Handle = *mut core::ffi::c_void;

    const PROCESS_QUERY_INFORMATION: u32 = 0x0400;
    const PROCESS_VM_READ: u32 = 0x0010;
    const PROCESS_DUP_HANDLE: u32 = 0x0040;
    const TH32CS_SNAPPROCESS: u32 = 0x00000002;
    const TH32CS_SNAPMODULE: u32 = 0x00000018;
    const FILE_TYPE_DISK: u32 = 1;
    const DUPLICATE_SAME_ACCESS: u32 = 2;
    const MEM_COMMIT: u32 = 0x1000;
    const PAGE_GUARD: u32 = 0x100;
    const PAGE_READABLE: u32 = 0xE6;
    const MAX_USER_ADDRESS: u64 = 140_737_488_355_328;
    const CHUNK: usize = 1_048_576;
    const READ_LIMIT: usize = 1_049_600;
    const SCAN_LIMIT: u64 = 4 * 1024 * 1024 * 1024;

    #[repr(C)]
    struct FileTime {
        low: u32,
        high: u32,
    }

    #[repr(C)]
    struct ProcessEntry32W {
        size: u32,
        usage: u32,
        process_id: u32,
        default_heap: usize,
        module_id: u32,
        threads: u32,
        parent: u32,
        priority: i32,
        flags: u32,
        exe: [u16; 260],
    }

    #[repr(C)]
    struct ModuleEntry32W {
        size: u32,
        module_id: u32,
        process_id: u32,
        global_usage: u32,
        process_usage: u32,
        base: *mut u8,
        base_size: u32,
        module: isize,
        name: [u16; 256],
        path: [u16; 260],
    }

    #[repr(C)]
    struct MemoryBasicInformation {
        base: u64,
        allocation_base: u64,
        allocation_protect: u32,
        region_size: u64,
        state: u32,
        protect: u32,
        kind: u32,
    }

    #[repr(C)]
    struct ByHandleFileInformation {
        attributes: u32,
        creation_low: u32,
        creation_high: u32,
        access_low: u32,
        access_high: u32,
        write_low: u32,
        write_high: u32,
        volume: u32,
        size_high: u32,
        size_low: u32,
        links: u32,
        index_high: u32,
        index_low: u32,
    }

    #[link(name = "kernel32")]
    extern "system" {
        fn OpenProcess(access: u32, inherit: i32, pid: u32) -> Handle;
        fn CloseHandle(handle: Handle) -> i32;
        fn CreateToolhelp32Snapshot(flags: u32, pid: u32) -> Handle;
        fn Process32FirstW(snapshot: Handle, entry: *mut ProcessEntry32W) -> i32;
        fn Process32NextW(snapshot: Handle, entry: *mut ProcessEntry32W) -> i32;
        fn Module32FirstW(snapshot: Handle, entry: *mut ModuleEntry32W) -> i32;
        fn Module32NextW(snapshot: Handle, entry: *mut ModuleEntry32W) -> i32;
        fn QueryFullProcessImageNameW(
            handle: Handle,
            flags: u32,
            path: *mut u16,
            size: *mut u32,
        ) -> i32;
        fn GetProcessTimes(
            handle: Handle,
            creation: *mut FileTime,
            exit: *mut FileTime,
            kernel: *mut FileTime,
            user: *mut FileTime,
        ) -> i32;
        fn ReadProcessMemory(
            handle: Handle,
            address: *const core::ffi::c_void,
            buffer: *mut core::ffi::c_void,
            length: usize,
            read: *mut usize,
        ) -> i32;
        fn VirtualQueryEx(
            handle: Handle,
            address: *const core::ffi::c_void,
            info: *mut MemoryBasicInformation,
            length: usize,
        ) -> usize;
        fn GetCurrentProcess() -> Handle;
        fn DuplicateHandle(
            process: Handle,
            source: Handle,
            target: Handle,
            output: *mut Handle,
            access: u32,
            inherit: i32,
            options: u32,
        ) -> i32;
        fn GetFileType(handle: Handle) -> u32;
        fn GetFinalPathNameByHandleW(
            handle: Handle,
            path: *mut u16,
            length: u32,
            flags: u32,
        ) -> u32;
        fn CreateFileW(
            name: *const u16,
            access: u32,
            share: u32,
            security: *mut core::ffi::c_void,
            creation: u32,
            flags: u32,
            template: Handle,
        ) -> Handle;
        fn GetFileInformationByHandle(handle: Handle, info: *mut ByHandleFileInformation) -> i32;
    }

    #[link(name = "ntdll")]
    extern "system" {
        fn NtQueryInformationProcess(
            handle: Handle,
            class: u32,
            buffer: *mut core::ffi::c_void,
            length: u32,
            returned: *mut u32,
        ) -> i32;
    }

    fn invalid(handle: Handle) -> bool {
        handle.is_null() || handle as usize == usize::MAX
    }

    fn wide(value: &str) -> Vec<u16> {
        OsStr::new(value)
            .encode_wide()
            .chain(std::iter::once(0))
            .collect()
    }

    fn from_wide(value: &[u16]) -> String {
        let end = value
            .iter()
            .position(|&item| item == 0)
            .unwrap_or(value.len());
        String::from_utf16_lossy(&value[..end])
    }

    fn normalize_path(value: &str) -> String {
        if let Some(rest) = value.strip_prefix(r"\\?\UNC\") {
            return format!(r"\\{rest}");
        }
        value.strip_prefix(r"\\?\").unwrap_or(value).to_string()
    }

    fn canonical(path: &Path) -> Result<PathBuf, String> {
        let value = fs::canonicalize(path).map_err(|_| "微信数据路径无效".to_string())?;
        Ok(PathBuf::from(normalize_path(&value.to_string_lossy())))
    }

    fn same_path(left: &Path, right: &Path) -> bool {
        left.as_os_str()
            .to_string_lossy()
            .eq_ignore_ascii_case(&right.as_os_str().to_string_lossy())
    }

    pub fn random_bytes(length: usize) -> Vec<u8> {
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_nanos();
        let stack = &now as *const _ as usize;
        let mut digest = Sha256::new();
        digest.update(now.to_le_bytes());
        digest.update(stack.to_le_bytes());
        digest.update(std::process::id().to_le_bytes());
        let value = digest.finalize();
        (0..length)
            .map(|index| value[index % value.len()])
            .collect()
    }

    fn file_identity(path: &Path) -> Result<String, String> {
        let name = wide(&path.to_string_lossy());
        let handle = unsafe {
            CreateFileW(
                name.as_ptr(),
                0x8000_0000,
                0x0000_0007,
                ptr::null_mut(),
                3,
                0x0200_0000,
                ptr::null_mut(),
            )
        };
        if invalid(handle) {
            return Err("微信数据文件身份读取失败".to_string());
        }
        let mut info: ByHandleFileInformation = unsafe { std::mem::zeroed() };
        let ok = unsafe { GetFileInformationByHandle(handle, &mut info) };
        unsafe {
            CloseHandle(handle);
        }
        if ok == 0 {
            return Err("微信数据文件身份读取失败".to_string());
        }
        Ok(format!(
            "{}:{}",
            info.volume,
            ((info.index_high as u64) << 32) | info.index_low as u64
        ))
    }

    pub struct ProcessMemory {
        handle: Handle,
        pid: u32,
        created: f64,
        exe: PathBuf,
        deadline: Instant,
        scanned: u64,
        module_base: u64,
        module_size: usize,
    }

    unsafe impl Send for ProcessMemory {}

    impl Drop for ProcessMemory {
        fn drop(&mut self) {
            if !invalid(self.handle) {
                unsafe {
                    CloseHandle(self.handle);
                }
                self.handle = ptr::null_mut();
            }
        }
    }

    impl ProcessMemory {
        fn open(pid: u32, expected: Option<f64>) -> Result<Self, String> {
            if pid == 0 {
                return Err("微信进程编号无效".to_string());
            }
            let handle = unsafe {
                OpenProcess(
                    PROCESS_QUERY_INFORMATION | PROCESS_VM_READ | PROCESS_DUP_HANDLE,
                    0,
                    pid,
                )
            };
            if invalid(handle) {
                return Err("无法只读打开所选微信进程".to_string());
            }
            let mut memory = Self {
                handle,
                pid,
                created: 0.0,
                exe: PathBuf::new(),
                deadline: Instant::now() + Duration::from_secs(30),
                scanned: 0,
                module_base: 0,
                module_size: 0,
            };
            let (exe, created) = memory.identity()?;
            memory.exe = exe;
            memory.created = created;
            let name = memory
                .exe
                .file_name()
                .and_then(OsStr::to_str)
                .unwrap_or_default();
            if !name.eq_ignore_ascii_case("weixin.exe")
                || expected
                    .map(|value| (value - created).abs() > 0.001)
                    .unwrap_or(false)
            {
                return Err("所选微信进程已变化".to_string());
            }
            Ok(memory)
        }

        fn identity(&self) -> Result<(PathBuf, f64), String> {
            let mut path = vec![0u16; 32768];
            let mut size = path.len() as u32;
            let mut creation: FileTime = unsafe { std::mem::zeroed() };
            let mut other: FileTime = unsafe { std::mem::zeroed() };
            let ok = unsafe {
                QueryFullProcessImageNameW(self.handle, 0, path.as_mut_ptr(), &mut size)
                    & GetProcessTimes(
                        self.handle,
                        &mut creation,
                        &mut other,
                        &mut other,
                        &mut other,
                    )
            };
            if ok == 0 {
                return Err("微信进程身份读取失败".to_string());
            }
            let created = (((creation.high as u64) << 32) | creation.low as u64) as f64
                / 10_000_000.0
                - 11_644_473_600.0;
            let exe = PathBuf::from(normalize_path(&from_wide(&path[..size as usize])));
            Ok((exe, created))
        }

        fn budget(&mut self, milliseconds: u64) {
            self.deadline = Instant::now() + Duration::from_millis(milliseconds);
            self.scanned = 0;
        }

        fn guard(&self) -> Result<(), String> {
            if invalid(self.handle) || Instant::now() > self.deadline {
                return Err("本机微信读取达到预算或会话已关闭".to_string());
            }
            let (exe, created) = self.identity()?;
            if !same_path(&exe, &self.exe) || (created - self.created).abs() > 0.001 {
                return Err("微信进程或账号已变化，请重新核验".to_string());
            }
            Ok(())
        }

        fn read(&mut self, address: u64, length: usize) -> Result<Option<Vec<u8>>, String> {
            self.guard()?;
            if address < 65_536
                || length == 0
                || length > READ_LIMIT
                || address.checked_add(length as u64).is_none()
            {
                return Err("微信只读内存范围无效".to_string());
            }
            self.scanned = self.scanned.saturating_add(length as u64);
            if self.scanned > SCAN_LIMIT {
                return Err("微信只读扫描达到预算".to_string());
            }
            let mut buffer = vec![0u8; length];
            let mut read = 0usize;
            let ok = unsafe {
                ReadProcessMemory(
                    self.handle,
                    address as *const core::ffi::c_void,
                    buffer.as_mut_ptr() as *mut core::ffi::c_void,
                    length,
                    &mut read,
                )
            };
            if ok == 0 || read != length {
                buffer.fill(0);
                return Ok(None);
            }
            Ok(Some(buffer))
        }

        fn find(&mut self, needle: &[u8]) -> Result<Vec<u64>, String> {
            if needle.is_empty() || needle.len() > 1024 {
                return Err("微信扫描标记无效".to_string());
            }
            let mut hits = Vec::new();
            let mut address = 0u64;
            let mut info: MemoryBasicInformation = unsafe { std::mem::zeroed() };
            while unsafe {
                VirtualQueryEx(
                    self.handle,
                    address as *const core::ffi::c_void,
                    &mut info,
                    std::mem::size_of::<MemoryBasicInformation>(),
                )
            } == std::mem::size_of::<MemoryBasicInformation>()
            {
                self.guard()?;
                if info.region_size == 0
                    || info.base.checked_add(info.region_size).is_none()
                    || info.base + info.region_size <= address
                {
                    break;
                }
                if info.state == MEM_COMMIT
                    && (info.protect & 0xFF & PAGE_READABLE) != 0
                    && (info.protect & PAGE_GUARD) == 0
                {
                    let mut offset = 0u64;
                    while offset < info.region_size {
                        let length = CHUNK
                            .saturating_add(needle.len() - 1)
                            .min((info.region_size - offset) as usize);
                        if let Some(buffer) = self.read(info.base + offset, length)? {
                            let mut position = 0usize;
                            while let Some(found) = find_bytes(&buffer, needle, position) {
                                if found >= CHUNK {
                                    break;
                                }
                                hits.push(info.base + offset + found as u64);
                                if hits.len() > 4096 {
                                    return Err("微信扫描标记超出预算".to_string());
                                }
                                position = found + 1;
                            }
                        }
                        offset += CHUNK as u64;
                    }
                }
                address = info.base + info.region_size;
                if address >= MAX_USER_ADDRESS {
                    break;
                }
            }
            Ok(hits)
        }

        fn load_module(&mut self) -> Result<(), String> {
            let snapshot = unsafe { CreateToolhelp32Snapshot(TH32CS_SNAPMODULE, self.pid) };
            if invalid(snapshot) {
                return Err("微信模块目录读取失败".to_string());
            }
            let mut entry: ModuleEntry32W = unsafe { std::mem::zeroed() };
            entry.size = std::mem::size_of::<ModuleEntry32W>() as u32;
            let mut ok = unsafe { Module32FirstW(snapshot, &mut entry) };
            let mut found = None;
            while ok != 0 {
                if from_wide(&entry.name).eq_ignore_ascii_case("weixin.dll") {
                    found = Some((entry.base as u64, entry.base_size as usize));
                    break;
                }
                ok = unsafe { Module32NextW(snapshot, &mut entry) };
            }
            unsafe {
                CloseHandle(snapshot);
            }
            let (base, size) = found.ok_or("未找到受支持的微信主模块")?;
            if base == 0 || size == 0 {
                return Err("微信主模块无效".to_string());
            }
            self.module_base = base;
            self.module_size = size;
            Ok(())
        }

        fn account_root(&mut self) -> Result<PathBuf, String> {
            self.guard()?;
            let mut buffer = vec![0u8; 65_536];
            let mut returned = 0u32;
            let mut status = unsafe {
                NtQueryInformationProcess(
                    self.handle,
                    51,
                    buffer.as_mut_ptr() as *mut core::ffi::c_void,
                    buffer.len() as u32,
                    &mut returned,
                )
            };
            if status < 0
                && returned as usize > buffer.len()
                && returned as usize <= 8 * 1024 * 1024
            {
                buffer.resize(returned as usize, 0);
                status = unsafe {
                    NtQueryInformationProcess(
                        self.handle,
                        51,
                        buffer.as_mut_ptr() as *mut core::ffi::c_void,
                        buffer.len() as u32,
                        &mut returned,
                    )
                };
            }
            if status < 0 || buffer.len() < 16 {
                return Err("无法核验微信打开的数据库".to_string());
            }
            let count = u64::from_le_bytes(buffer[0..8].try_into().unwrap_or([0; 8]));
            if count > 65_536 || 16 + count as usize * 40 > buffer.len() {
                return Err("微信数据库句柄达到预算".to_string());
            }
            let mut roots = HashSet::new();
            for index in 0..count as usize {
                self.guard()?;
                let offset = 16 + index * 40;
                let source =
                    u64::from_le_bytes(buffer[offset..offset + 8].try_into().unwrap_or([0; 8]))
                        as usize as Handle;
                if invalid(source) {
                    continue;
                }
                let mut duplicate: Handle = ptr::null_mut();
                let ok = unsafe {
                    DuplicateHandle(
                        self.handle,
                        source,
                        GetCurrentProcess(),
                        &mut duplicate,
                        0,
                        0,
                        DUPLICATE_SAME_ACCESS,
                    )
                };
                if ok == 0 || invalid(duplicate) {
                    continue;
                }
                if unsafe { GetFileType(duplicate) } == FILE_TYPE_DISK {
                    let mut path = vec![0u16; 32768];
                    let length = unsafe {
                        GetFinalPathNameByHandleW(duplicate, path.as_mut_ptr(), 32768, 0)
                    };
                    if length > 0 && length < 32768 {
                        let value = normalize_path(&from_wide(&path[..length as usize]));
                        let lower = value.to_ascii_lowercase();
                        if let Some(position) = lower.rfind(r"\db_storage\") {
                            let suffix = &value[position + 12..];
                            if suffix.len() > 3 && suffix.to_ascii_lowercase().ends_with(".db") {
                                if let Ok(root) = canonical(Path::new(&value[..position])) {
                                    roots.insert(root);
                                }
                            }
                        }
                    }
                }
                unsafe {
                    CloseHandle(duplicate);
                }
            }
            if roots.len() != 1 {
                return Err("所选微信进程未对应唯一账号目录".to_string());
            }
            Ok(roots.into_iter().next().unwrap())
        }

        fn read_identity(&mut self) -> Result<String, String> {
            let base = self.module_base;
            let size = self.module_size;
            if base == 0 || size == 0 || size >= 268_435_456 {
                return Ok(String::new());
            }
            let mut position = None;
            let last = ((size.saturating_sub(1)) / CHUNK) * CHUNK;
            let mut offset = last;
            loop {
                self.guard()?;
                let length = CHUNK.saturating_add(32).min(size - offset);
                if let Some(image) = self.read(base + offset as u64, length)? {
                    let limit = (CHUNK.min(image.len().saturating_sub(16)) / 8) * 8;
                    let mut index = limit;
                    while index >= 16 {
                        if index % 8192 == 0 {
                            self.guard()?;
                        }
                        let first = u32::from_le_bytes(
                            image[index..index + 4].try_into().unwrap_or([0; 4]),
                        );
                        let second = u32::from_le_bytes(
                            image[index + 8..index + 12].try_into().unwrap_or([0; 4]),
                        );
                        if first == 13
                            && (second | 15) == 15
                            && image[index - 16..index - 3] == *b"global_config"
                        {
                            position = Some(offset + index);
                            break;
                        }
                        index -= 8;
                    }
                }
                if position.is_some() || offset == 0 {
                    break;
                }
                offset = offset.saturating_sub(CHUNK);
            }
            let position = position.ok_or("未找到微信账号身份")?;
            let first = self
                .read(base + position as u64 - 312, 8)?
                .ok_or("微信账号身份读取失败")?;
            let p1 = pointer(&first, 0).ok_or("微信账号身份读取失败")?;
            let second = self.read(p1 + 104, 8)?.ok_or("微信账号身份读取失败")?;
            let config = pointer(&second, 0).ok_or("微信账号身份读取失败")?;
            let layout = self.read(config + 72, 32)?.ok_or("微信账号身份读取失败")?;
            let length = u64::from_le_bytes(layout[16..24].try_into().unwrap_or([0; 8]));
            if length == 0 || length > 200 {
                return Err("微信账号身份长度无效".to_string());
            }
            let bytes = if length <= 15 {
                layout[..length as usize].to_vec()
            } else {
                let location = pointer(&layout, 0).ok_or("微信账号身份读取失败")?;
                self.read(location, length as usize)?
                    .ok_or("微信账号身份读取失败")?
            };
            String::from_utf8(bytes).map_err(|_| "微信账号身份编码无效".to_string())
        }

        fn close(&mut self) {
            if !invalid(self.handle) {
                unsafe {
                    CloseHandle(self.handle);
                }
                self.handle = ptr::null_mut();
            }
        }
    }

    fn pointer(data: &[u8], offset: usize) -> Option<u64> {
        if data.len() < offset + 8 {
            return None;
        }
        let value = u64::from_le_bytes(data[offset..offset + 8].try_into().ok()?);
        (65_536..MAX_USER_ADDRESS).contains(&value).then_some(value)
    }

    fn find_bytes(haystack: &[u8], needle: &[u8], start: usize) -> Option<usize> {
        if start >= haystack.len() || needle.is_empty() {
            return None;
        }
        haystack[start..]
            .windows(needle.len())
            .position(|window| window == needle)
            .map(|value| value + start)
    }

    pub fn processes() -> Result<Value, String> {
        let snapshot = unsafe { CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0) };
        if invalid(snapshot) {
            return Err("本机微信进程目录不可用".to_string());
        }
        let mut entry: ProcessEntry32W = unsafe { std::mem::zeroed() };
        entry.size = std::mem::size_of::<ProcessEntry32W>() as u32;
        let mut ok = unsafe { Process32FirstW(snapshot, &mut entry) };
        let mut values = Vec::new();
        while ok != 0 {
            if from_wide(&entry.exe).eq_ignore_ascii_case("weixin.exe") {
                match ProcessMemory::open(entry.process_id, None) {
                    Ok(mut memory) => {
                        values.push(json!({
                            "pid": memory.pid,
                            "created": memory.created,
                            "label": format!("本机微信 · PID {}", memory.pid)
                        }));
                        memory.close();
                    }
                    Err(_) => {}
                }
                if values.len() >= 16 {
                    break;
                }
            }
            ok = unsafe { Process32NextW(snapshot, &mut entry) };
        }
        unsafe {
            CloseHandle(snapshot);
        }
        Ok(json!({
            "processes": values,
            "coverage": "local_processes_only",
            "keys_scanned": false
        }))
    }

    pub struct Reader {
        id: String,
        memory: ProcessMemory,
        account: String,
        root: PathBuf,
        base: PathBuf,
        base_id: String,
        keys: HashMap<PathBuf, Vec<u8>>,
        identities: HashMap<PathBuf, String>,
        deadline: Instant,
    }

    unsafe impl Send for Reader {}

    impl Drop for Reader {
        fn drop(&mut self) {
            for key in self.keys.values_mut() {
                key.fill(0);
            }
            self.keys.clear();
            self.memory.close();
        }
    }

    impl Reader {
        pub fn open(pid: u32, created: Option<f64>) -> Result<Self, String> {
            let mut memory = ProcessMemory::open(pid, created)?;
            memory.budget(30_000);
            let deadline = Instant::now() + Duration::from_secs(120);
            let root = memory.account_root()?;
            let base = root.join("db_storage");
            let base_id = file_identity(&base)?;
            memory.load_module()?;
            let account = memory.read_identity()?;
            let directory = root.file_name().and_then(OsStr::to_str).unwrap_or_default();
            let expected = directory
                .rsplit_once('_')
                .filter(|(_, suffix)| {
                    suffix.chars().count() == 4
                        && suffix
                            .chars()
                            .all(|value| value.is_ascii_alphanumeric() || value == '_')
                })
                .map(|(value, _)| value.to_string())
                .unwrap_or_else(|| directory.to_string());
            if account.is_empty() || account != expected {
                return Err("微信进程身份与账号目录不一致".to_string());
            }
            let mut reader = Self {
                id: super::random_id(),
                memory,
                account,
                root,
                base,
                base_id,
                keys: HashMap::new(),
                identities: HashMap::new(),
                deadline,
            };
            let files = reader.files()?;
            let mut pages = HashMap::new();
            for file in &files {
                reader.identities.insert(file.clone(), file_identity(file)?);
                let mut page = vec![0u8; 4096];
                let mut handle = File::open(file).map_err(|_| "微信库文件不完整".to_string())?;
                handle
                    .read_exact(&mut page)
                    .map_err(|_| "微信库文件不完整".to_string())?;
                pages.insert(file.clone(), page);
            }
            let keys = scan_keys(&mut reader.memory, &pages);
            for page in pages.values_mut() {
                page.fill(0);
            }
            reader.keys = keys;
            if files.iter().any(|file| !reader.keys.contains_key(file)) {
                return Err(format!(
                    "部分微信消息库未取得已核验密钥（{}/{}），请重新核验",
                    reader.keys.len(),
                    files.len()
                ));
            }
            if files.first().is_none() {
                return Err("微信联系人库不存在".to_string());
            }
            let account = reader.account.clone();
            let found = reader.query(files[0].clone(), |db| {
                let mut statement = db
                    .prepare("SELECT 1 FROM contact WHERE username=? LIMIT 1")
                    .map_err(|_| "微信联系人库查询失败".to_string())?;
                Ok(statement.query_row([account.as_str()], |_| Ok(())).is_ok())
            })?;
            if !found {
                return Err("微信联系人库未确认当前身份".to_string());
            }
            reader.fence(true)?;
            Ok(reader)
        }

        pub fn reader_id(&self) -> &str {
            &self.id
        }

        pub fn account(&self) -> &str {
            &self.account
        }

        fn guard(&self) -> Result<(), String> {
            if Instant::now() > self.deadline {
                return Err("微信只读任务达到预算".to_string());
            }
            self.memory.guard()
        }

        fn path(&self, file: &Path) -> Result<(), String> {
            self.guard()?;
            if !same_path(&self.base, &canonical(&self.base)?)
                || file_identity(&self.base)? != self.base_id
                || !same_path(file, &canonical(file)?)
                || file.strip_prefix(&self.base).is_err()
            {
                return Err("微信数据库路径已变化".to_string());
            }
            let mut current = file.to_path_buf();
            loop {
                let metadata =
                    fs::symlink_metadata(&current).map_err(|_| "微信数据库路径无效".to_string())?;
                if metadata.file_type().is_symlink() {
                    return Err("微信数据库不能经过链接路径".to_string());
                }
                if same_path(&current, &self.root) {
                    break;
                }
                current = current
                    .parent()
                    .ok_or("微信数据库路径不属于账号")?
                    .to_path_buf();
            }
            if let Some(identity) = self.identities.get(file) {
                if file_identity(file)? != *identity {
                    return Err("微信数据库已替换，请重新核验".to_string());
                }
            }
            Ok(())
        }

        fn files(&self) -> Result<Vec<PathBuf>, String> {
            let contact = self.base.join("contact").join("contact.db");
            let mut shards = Vec::new();
            for item in fs::read_dir(self.base.join("message"))
                .map_err(|_| "微信消息分片目录不可用".to_string())?
            {
                let path = item
                    .map_err(|_| "微信消息分片目录不可用".to_string())?
                    .path();
                let name = path.file_name().and_then(OsStr::to_str).unwrap_or_default();
                if let Some(number) = name
                    .strip_prefix("message_")
                    .and_then(|value| value.strip_suffix(".db"))
                {
                    if !number.is_empty() && number.chars().all(|value| value.is_ascii_digit()) {
                        shards.push(path);
                    }
                }
            }
            shards.sort();
            if shards.len() > 64 {
                return Err("微信消息分片超出预算".to_string());
            }
            let mut files = vec![contact];
            files.extend(shards);
            if self
                .identities
                .keys()
                .any(|file| !files.iter().any(|item| same_path(item, file)))
            {
                return Err("微信消息分片缺失或超出预算".to_string());
            }
            for file in &files {
                self.path(file)?;
                if !fs::metadata(file)
                    .map(|value| value.is_file())
                    .unwrap_or(false)
                {
                    return Err("微信数据库文件无效".to_string());
                }
            }
            Ok(files)
        }

        fn fence(&mut self, strong: bool) -> Result<(), String> {
            self.guard()?;
            if strong && !same_path(&self.memory.account_root()?, &self.root) {
                return Err("微信账号目录已改变".to_string());
            }
            self.path(&self.base.join("contact").join("contact.db"))
        }

        pub fn verify(&mut self, account: &str) -> Result<(), String> {
            self.memory.budget(10_000);
            self.deadline = Instant::now() + Duration::from_secs(10);
            if account != self.account || self.id.is_empty() {
                return Err("本机微信绑定已失效".to_string());
            }
            self.fence(true)
        }

        fn query<T>(
            &mut self,
            file: PathBuf,
            read: impl FnOnce(&Connection) -> Result<T, String>,
        ) -> Result<T, String> {
            self.fence(false)?;
            self.path(&file)?;
            let key = self
                .keys
                .get(&file)
                .ok_or("所选微信库缺少独立密钥")?
                .clone();
            let result = read_sqlcipher(&file, &key, read);
            self.path(&file)?;
            self.fence(false)?;
            result
        }

        pub fn directory(&mut self) -> Result<Value, String> {
            self.verify(&self.account.clone())?;
            let file = self.files()?.first().cloned().ok_or("微信联系人库不存在")?;
            let account = self.account.clone();
            self.query(file, |db| {
                let mut statement = db
                    .prepare(
                        "SELECT username,substr(nick_name,1,200) AS nick,substr(remark,1,200) AS remark \
                         FROM contact WHERE username<>? AND length(username)<=200 ORDER BY username LIMIT 5001",
                    )
                    .map_err(|_| "微信联系人库查询失败".to_string())?;
                let mut rows = statement
                    .query([account.as_str()])
                    .map_err(|_| "微信联系人库查询失败".to_string())?;
                let mut peers = Vec::new();
                let mut total = 0usize;
                while let Some(row) = rows.next().map_err(|_| "微信联系人库查询失败".to_string())? {
                    total += 1;
                    if peers.len() >= 5000 {
                        continue;
                    }
                    let username: String = row.get(0).unwrap_or_default();
                    if username.is_empty() || username.chars().count() > 200 {
                        continue;
                    }
                    let nick: String = row.get(1).unwrap_or_default();
                    let remark: String = row.get(2).unwrap_or_default();
                    let name = if !remark.is_empty() {
                        remark
                    } else if !nick.is_empty() {
                        nick
                    } else {
                        username.clone()
                    };
                    peers.push(json!({"peer": username, "name": name}));
                }
                Ok(json!({
                    "peers": peers,
                    "coverage": if total > 5000 { "partial_page_limit" } else { "local_contact_table" }
                }))
            })
        }

        pub fn receive(&mut self, peer: &str, session: &Value) -> Result<Value, String> {
            if peer.is_empty() || peer.chars().count() > 200 || peer == "_directory_" {
                return Err("选择的微信会话无效".to_string());
            }
            let cursors = super::parse_cursor(Some(session), &self.id)?;
            let cursors = cursors.as_object().ok_or("保存的微信消息游标无效")?.clone();
            self.verify(&self.account.clone())?;
            self.memory.budget(15_000);
            self.deadline = Instant::now() + Duration::from_secs(15);
            let files = self.files()?;
            let contact = files.first().cloned().ok_or("微信联系人库不存在")?;
            let found = self.query(contact, |db| {
                let mut statement = db
                    .prepare("SELECT 1 FROM contact WHERE username=? LIMIT 1")
                    .map_err(|_| "微信联系人库查询失败".to_string())?;
                Ok(statement.query_row([peer], |_| Ok(())).is_ok())
            })?;
            if !found {
                return Err("所选会话不在当前微信联系人库".to_string());
            }
            let shards = &files[1..];
            let prior = cursors
                .get(peer)
                .and_then(Value::as_object)
                .cloned()
                .unwrap_or_default();
            if prior.keys().any(|name| {
                !shards.iter().any(|path| {
                    path.file_name()
                        .and_then(OsStr::to_str)
                        .map(|value| value == name)
                        .unwrap_or(false)
                })
            }) {
                return Err("微信已保存游标对应分片缺失".to_string());
            }
            let mut updates = prior.clone();
            let table = format!("Msg_{}", hex_digest::<Md5>(peer.as_bytes()));
            let mut messages = Vec::new();
            let mut limited = false;
            let take = 50usize.min(2usize.max(200 / shards.len().max(1)));
            for file in shards {
                self.guard()?;
                let name = file
                    .file_name()
                    .and_then(OsStr::to_str)
                    .unwrap_or_default()
                    .to_string();
                let watermark = prior.get(&name).and_then(parse_watermark);
                let batch = self.query(file.clone(), |db| {
                    let exists: bool = db
                        .prepare("SELECT 1 FROM sqlite_master WHERE type='table' AND name=? LIMIT 1")
                        .and_then(|mut statement| statement.query_row([table.as_str()], |_| Ok(())))
                        .is_ok();
                    if !exists {
                        return Ok(Vec::new());
                    }
                    let mut rows = Vec::new();
                    if let Some((sequence, local)) = watermark {
                        let mut statement = db
                            .prepare(&format!(
                                "SELECT {FIELDS} FROM \"{table}\" WHERE sort_seq>? OR (sort_seq=? AND local_id>?) \
                                 ORDER BY sort_seq ASC,local_id ASC LIMIT ?"
                            ))
                            .map_err(|_| "微信消息库查询失败".to_string())?;
                        let mut selected = statement
                            .query(rusqlite::params![sequence, sequence, local, take as i64 + 1])
                            .map_err(|_| "微信消息库查询失败".to_string())?;
                        while let Some(row) = selected
                            .next()
                            .map_err(|_| "微信消息库查询失败".to_string())?
                        {
                            rows.push(MessageRow::from_row(row)?);
                        }
                        if rows.len() > take {
                            limited = true;
                        }
                        let newer_count = rows.len().max(1).min(take / 2);
                        let half = if rows.len() > newer_count {
                            limited = true;
                            newer_count
                        } else {
                            rows.len()
                        };
                        rows.truncate(half);
                        let remaining = take.saturating_sub(rows.len());
                        if remaining > 0 {
                            let mut overlap = db
                                .prepare(&format!(
                                    "SELECT {FIELDS} FROM \"{table}\" WHERE sort_seq<? OR (sort_seq=? AND local_id<=?) \
                                     ORDER BY sort_seq DESC,local_id DESC LIMIT ?"
                                ))
                                .map_err(|_| "微信消息库查询失败".to_string())?;
                            let mut selected = overlap
                                .query(rusqlite::params![sequence, sequence, local, remaining as i64])
                                .map_err(|_| "微信消息库查询失败".to_string())?;
                            while let Some(row) = selected
                                .next()
                                .map_err(|_| "微信消息库查询失败".to_string())?
                            {
                                rows.push(MessageRow::from_row(row)?);
                            }
                        }
                    } else {
                        let mut statement = db
                            .prepare(&format!(
                                "SELECT {FIELDS} FROM \"{table}\" ORDER BY sort_seq DESC,local_id DESC LIMIT ?"
                            ))
                            .map_err(|_| "微信消息库查询失败".to_string())?;
                        let mut selected = statement
                            .query([take as i64 + 1])
                            .map_err(|_| "微信消息库查询失败".to_string())?;
                        while let Some(row) = selected
                            .next()
                            .map_err(|_| "微信消息库查询失败".to_string())?
                        {
                            rows.push(MessageRow::from_row(row)?);
                        }
                        if rows.len() > take {
                            limited = true;
                            rows.truncate(take);
                        }
                    }
                    let mut values = Vec::new();
                    for row in rows {
                        let sender = db
                            .prepare("SELECT substr(user_name,1,200) FROM Name2Id WHERE rowid=? LIMIT 1")
                            .and_then(|mut statement| {
                                statement.query_row([row.sender], |value| value.get::<_, String>(0))
                            })
                            .unwrap_or_default();
                        let text = if row.oversized {
                            "[压缩消息超出读取范围]".to_string()
                        } else {
                            weixin_text(&row)
                        };
                        let mid = format!(
                            "wx:{}",
                            hex_digest::<Sha256>(
                                format!("{}:{}", name, row.local).as_bytes()
                            )
                        );
                        values.push((
                            row.sequence,
                            row.local,
                            json!({
                                "mid": mid,
                                "sender": sender,
                                "ts": row.created.max(0) as f64,
                                "text": text,
                                "kind": if row.kind == 1 { "text" } else { "unsupported_media" }
                            }),
                        ));
                    }
                    Ok(values)
                })?;
                for (sequence, local, message) in batch {
                    if updates
                        .get(&name)
                        .and_then(parse_watermark)
                        .map(|(old_sequence, old_local)| {
                            sequence > old_sequence
                                || (sequence == old_sequence && local > old_local)
                        })
                        .unwrap_or(true)
                    {
                        updates.insert(name.clone(), json!([sequence, local]));
                    }
                    messages.push(message);
                }
                let text_size: usize = messages
                    .iter()
                    .map(|value| {
                        value
                            .get("text")
                            .and_then(Value::as_str)
                            .unwrap_or_default()
                            .chars()
                            .count()
                    })
                    .sum();
                if messages.len() > 200 || text_size > 100_000 {
                    return Err("本次微信消息超过读取预算".to_string());
                }
            }
            if updates.len() > 100 {
                return Err("微信所选会话达到上限".to_string());
            }
            self.fence(true)?;
            let cursors = updates
                .into_iter()
                .map(|(name, value)| {
                    let pair = value
                        .as_array()
                        .map(|items| {
                            items
                                .iter()
                                .map(|item| {
                                    if let Some(number) = item.as_i64() {
                                        json!(number.to_string())
                                    } else {
                                        item.clone()
                                    }
                                })
                                .collect::<Vec<_>>()
                        })
                        .unwrap_or_default();
                    (name, Value::Array(pair))
                })
                .collect::<Map<String, Value>>();
            messages.sort_by(|left, right| {
                let left_ts = left.get("ts").and_then(Value::as_f64).unwrap_or(0.0);
                let right_ts = right.get("ts").and_then(Value::as_f64).unwrap_or(0.0);
                left_ts
                    .partial_cmp(&right_ts)
                    .unwrap_or(std::cmp::Ordering::Equal)
                    .then_with(|| {
                        left.get("mid")
                            .and_then(Value::as_str)
                            .unwrap_or_default()
                            .cmp(right.get("mid").and_then(Value::as_str).unwrap_or_default())
                    })
            });
            Ok(json!({
                "messages": messages,
                "session": {"reader_id": self.id, "cursors": cursors},
                "coverage": {
                    "state": if limited { "bounded_window" } else { "scanned_local_shards" },
                    "shards": shards.len(),
                    "initial_recent_window": prior.is_empty(),
                    "older_history": "not_complete"
                }
            }))
        }
    }

    const FIELDS: &str = "CASE WHEN length(CAST(message_content AS BLOB))<=24000 THEN message_content END AS raw,\
        local_type AS kind,real_sender_id AS sender,sort_seq AS seq,local_id AS local,\
        CASE WHEN length(compress_content)<=24000 THEN compress_content END AS compressed,\
        create_time AS created,CASE WHEN length(compress_content)>24000 THEN 1 ELSE 0 END AS oversized";

    struct MessageRow {
        raw: Option<Vec<u8>>,
        compressed: Option<Vec<u8>>,
        kind: i64,
        sender: i64,
        sequence: i64,
        local: i64,
        created: i64,
        oversized: bool,
    }

    impl MessageRow {
        fn from_row(row: &rusqlite::Row<'_>) -> Result<Self, String> {
            let raw = value_bytes(row.get_ref(0).map_err(|_| "微信消息字段无效")?);
            let kind = row.get::<_, i64>(1).unwrap_or(0);
            let sender = row.get::<_, i64>(2).unwrap_or(0);
            let sequence = row.get::<_, i64>(3).unwrap_or(0);
            let local = row.get::<_, i64>(4).unwrap_or(0);
            let compressed = value_bytes(row.get_ref(5).map_err(|_| "微信消息字段无效")?);
            let created = row.get::<_, i64>(6).unwrap_or(0);
            let oversized = row.get::<_, i64>(7).unwrap_or(0) != 0;
            Ok(Self {
                raw,
                compressed,
                kind,
                sender,
                sequence,
                local,
                created,
                oversized,
            })
        }
    }

    fn value_bytes(value: ValueRef<'_>) -> Option<Vec<u8>> {
        match value {
            ValueRef::Blob(value) => Some(value.to_vec()),
            ValueRef::Text(value) => Some(value.to_vec()),
            _ => None,
        }
    }

    fn weixin_text(row: &MessageRow) -> String {
        if row.kind != 1 {
            return "[暂未读取此类消息内容]".to_string();
        }
        let mut bytes = row
            .compressed
            .clone()
            .or_else(|| row.raw.clone())
            .unwrap_or_default();
        if bytes.starts_with(&[0x28, 0xb5, 0x2f, 0xfd]) {
            match zstd::bulk::decompress(&bytes, 24_000) {
                Ok(value) => bytes = value,
                Err(_) => return "[消息编码暂不支持或超出读取范围]".to_string(),
            }
        }
        match String::from_utf8(bytes) {
            Ok(value) => value.trim().chars().take(6000).collect(),
            Err(_) => "[消息编码暂不支持或超出读取范围]".to_string(),
        }
    }

    fn parse_watermark(value: &Value) -> Option<(i64, i64)> {
        let items = value.as_array()?;
        if items.len() != 2 {
            return None;
        }
        let first = items[0]
            .as_i64()
            .or_else(|| items[0].as_str().and_then(|value| value.parse().ok()))?;
        let second = items[1]
            .as_i64()
            .or_else(|| items[1].as_str().and_then(|value| value.parse().ok()))?;
        Some((first, second))
    }

    fn hex_digest<D: Digest + Default>(value: &[u8]) -> String {
        let mut digest = D::new();
        digest.update(value);
        digest
            .finalize()
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect()
    }

    fn read_sqlcipher<T>(
        path: &Path,
        key: &[u8],
        read: impl FnOnce(&Connection) -> Result<T, String>,
    ) -> Result<T, String> {
        if key.len() != 32 && key.len() != 48 {
            return Err("所选微信库缺少已核验密钥".to_string());
        }
        let connection = Connection::open_with_flags(
            path,
            OpenFlags::SQLITE_OPEN_READ_ONLY | OpenFlags::SQLITE_OPEN_NO_MUTEX,
        )
        .map_err(|_| "本次微信只读事务未完成，未保存消息或推进游标".to_string())?;
        let setup = (|| -> Result<(), String> {
            connection
                .execute_batch("PRAGMA cipher='sqlcipher'; PRAGMA legacy=4;")
                .map_err(|_| "微信 SQLCipher 配置失败".to_string())?;
            if key.len() == 48 {
                connection
                    .execute_batch("PRAGMA plaintext_header_size=16;")
                    .map_err(|_| "微信 SQLCipher 配置失败".to_string())?;
            }
            let statement = format!("PRAGMA key = \"x'{}'\";", key_hex(key));
            connection
                .execute_batch(&statement)
                .map_err(|_| "微信 SQLCipher 密钥无效".to_string())?;
            connection
                .execute_batch(
                    "PRAGMA query_only=ON; PRAGMA temp_store=MEMORY; PRAGMA cache_size=-4096; \
                     PRAGMA mmap_size=0; PRAGMA trusted_schema=OFF; BEGIN;",
                )
                .map_err(|_| "微信 SQLCipher 只读配置失败".to_string())?;
            Ok(())
        })();
        if let Err(error) = setup {
            let _ = connection.execute_batch("ROLLBACK;");
            return Err(error);
        }
        let result = read(&connection);
        let _ = connection.execute_batch("ROLLBACK;");
        result.map_err(|_| "本次微信只读事务未完成，未保存消息或推进游标".to_string())
    }

    fn verify_key(key: &[u8], page: &[u8]) -> bool {
        if (key.len() != 32 && key.len() != 48) || page.len() != 4096 {
            return false;
        }
        let salt = if key.len() == 48 {
            key[32..48].to_vec()
        } else {
            page[..16].to_vec()
        };
        let mac_salt = salt.iter().map(|value| value ^ 0x3A).collect::<Vec<u8>>();
        let mut mac_key = [0u8; 32];
        pbkdf2_hmac::<Sha512>(&key[..32], &mac_salt, 2, &mut mac_key);
        let mut mac = match Hmac::<Sha512>::new_from_slice(&mac_key) {
            Ok(value) => value,
            Err(_) => {
                mac_key.fill(0);
                return false;
            }
        };
        mac.update(&page[16..4032]);
        mac.update(&[1, 0, 0, 0]);
        let valid = mac.verify_slice(&page[4032..]).is_ok();
        mac_key.fill(0);
        valid
    }

    fn scan_keys(
        memory: &mut ProcessMemory,
        pages: &HashMap<PathBuf, Vec<u8>>,
    ) -> HashMap<PathBuf, Vec<u8>> {
        let marker = b"com.Tencent.WCDB.Config.Cipher";
        let mask = hex_bytes("d2c7442458020000004889442450488b450048844c2448488944254048584c24");
        let mut keys = HashMap::new();
        let mut tested = HashSet::new();
        let debug = std::env::var_os("ZYNOTE_WECHAT_DEBUG").is_some();
        let _ = memory.budget(30_000);
        let needles = match memory.find(marker) {
            Ok(value) => value,
            Err(error) => {
                if debug {
                    eprintln!("wechat scan: marker scan failed: {error}");
                }
                return keys;
            }
        };
        let mut locations_count = 0usize;
        let mut nodes_count = 0usize;
        let mut runs_count = 0usize;
        let mut candidates_count = 0usize;
        let markers_count = needles.len();
        let needle_set = needles.iter().copied().collect::<HashSet<u64>>();
        'outer: for needle in needles {
            let _ = memory.budget(30_000);
            let mut pair = Vec::with_capacity(16);
            pair.extend_from_slice(&needle.to_le_bytes());
            pair.extend_from_slice(&(marker.len() as u64).to_le_bytes());
            let locations = match memory.find(&pair) {
                Ok(value) => value,
                Err(error) => {
                    if debug {
                        eprintln!("wechat scan: pair scan failed: {error}");
                    }
                    break;
                }
            };
            locations_count += locations.len();
            for at in locations {
                if memory.guard().is_err() {
                    break 'outer;
                }
                let node = match at
                    .checked_sub(16)
                    .and_then(|start| memory.read(start, 80).ok())
                    .flatten()
                {
                    Some(value) => value,
                    None => continue,
                };
                if node.len() < 64
                    || !needle_set.contains(&pointer(&node, 16).unwrap_or(0))
                    || u64::from_le_bytes(node[24..32].try_into().unwrap_or([0; 8]))
                        != marker.len() as u64
                {
                    continue;
                }
                nodes_count += 1;
                let config = match pointer(&node, 40) {
                    Some(value) => value,
                    None => continue,
                };
                let object = match memory.read(config + 136, 40).ok().flatten() {
                    Some(value) => value,
                    None => continue,
                };
                let location = match pointer(&object, 8) {
                    Some(value) => value,
                    None => continue,
                };
                let length = u64::from_le_bytes(object[16..24].try_into().unwrap_or([0; 8]));
                if length == 0 || length > 1024 {
                    continue;
                }
                let encrypted = match memory.read(location, length as usize).ok().flatten() {
                    Some(value) => value,
                    None => continue,
                };
                let mut decoded = encrypted
                    .iter()
                    .enumerate()
                    .map(|(index, value)| value ^ mask[index % mask.len()])
                    .collect::<Vec<u8>>();
                let runs = hex_runs(&decoded);
                runs_count += runs.len();
                for run in runs {
                    let mut starts = vec![0usize];
                    if run.len() > 96 {
                        let mut offset = 0usize;
                        while offset + 64 <= run.len() {
                            starts.push(offset);
                            offset += 32;
                        }
                        starts.push(run.len() - 64);
                    }
                    starts.sort_unstable();
                    starts.dedup();
                    for start in starts {
                        if memory.guard().is_err() {
                            break;
                        }
                        let Some(candidate) = hex_bytes_checked(&run[start..start + 64]) else {
                            continue;
                        };
                        if candidate.iter().copied().collect::<HashSet<u8>>().len() < 15 {
                            continue;
                        }
                        let fingerprint = key_hex(&candidate);
                        if !tested.insert(fingerprint) {
                            continue;
                        }
                        candidates_count += 1;
                        if tested.len() > 4096 {
                            break 'outer;
                        }
                        let mut choices = vec![candidate.clone()];
                        if start + 96 <= run.len() {
                            if let Some(extra) = hex_bytes_checked(&run[start + 64..start + 96]) {
                                let mut combined = candidate.clone();
                                combined.extend_from_slice(&extra);
                                choices.push(combined);
                            }
                        }
                        for choice in &choices {
                            for (path, page) in pages {
                                if !keys.contains_key(path) && verify_key(choice, page) {
                                    keys.insert(path.clone(), choice.clone());
                                }
                            }
                        }
                        for choice in choices.iter_mut() {
                            choice.fill(0);
                        }
                        let mut candidate = candidate;
                        candidate.fill(0);
                    }
                }
                decoded.fill(0);
                let mut encrypted = encrypted;
                encrypted.fill(0);
                if keys.len() == pages.len() {
                    break 'outer;
                }
            }
        }
        if debug {
            eprintln!(
                "wechat scan: markers={} locations={} nodes={} runs={} candidates={} keys={}/{}",
                markers_count,
                locations_count,
                nodes_count,
                runs_count,
                candidates_count,
                keys.len(),
                pages.len()
            );
        }
        if keys.len() != pages.len() {
            for key in keys.values_mut() {
                key.fill(0);
            }
            keys.clear();
        }
        keys
    }

    fn hex_bytes(value: &str) -> Vec<u8> {
        value
            .as_bytes()
            .chunks_exact(2)
            .filter_map(|pair| {
                std::str::from_utf8(pair)
                    .ok()
                    .and_then(|text| u8::from_str_radix(text, 16).ok())
            })
            .collect()
    }

    fn hex_bytes_checked(value: &[u8]) -> Option<Vec<u8>> {
        if value.len() % 2 != 0 {
            return None;
        }
        let text = std::str::from_utf8(value).ok()?;
        let mut result = Vec::with_capacity(value.len() / 2);
        for index in (0..text.len()).step_by(2) {
            result.push(u8::from_str_radix(&text[index..index + 2], 16).ok()?);
        }
        Some(result)
    }

    fn hex_runs(value: &[u8]) -> Vec<Vec<u8>> {
        let mut runs = Vec::new();
        let mut index = 0usize;
        while index + 3 < value.len() {
            if (value[index] == b'x' || value[index] == b'X') && value[index + 1] == b'\'' {
                let start = index + 2;
                let mut end = start;
                while end < value.len() && value[end].is_ascii_hexdigit() {
                    end += 1;
                }
                let length = end - start;
                if (64..=192).contains(&length) && end < value.len() && value[end] == b'\'' {
                    runs.push(value[start..end].to_vec());
                    index = end + 1;
                    continue;
                }
            }
            index += 1;
        }
        runs
    }

    #[cfg(test)]
    mod tests {
        use super::*;

        #[test]
        fn key_hex_round_trip() {
            let bytes = (0u8..32).collect::<Vec<_>>();
            assert_eq!(hex_bytes(&key_hex(&bytes)), bytes);
        }

        #[test]
        fn rejects_non_sqlcipher_page() {
            assert!(!verify_key(&[7u8; 32], &[0u8; 4096]));
        }

        #[test]
        fn read_only_sqlcipher_fixture_round_trip() {
            let directory =
                std::env::temp_dir().join(format!("zynote-sqlcipher-{}", std::process::id()));
            let _ = std::fs::remove_dir_all(&directory);
            std::fs::create_dir_all(&directory).unwrap();
            let path = directory.join("fixture.db");
            let key = (0u8..32).collect::<Vec<_>>();
            {
                let connection = Connection::open(&path).unwrap();
                connection
                    .execute_batch(&format!(
                        "PRAGMA key = \"x'{}'\"; PRAGMA cipher='sqlcipher'; PRAGMA legacy=4; \
                         CREATE TABLE contact(username TEXT); INSERT INTO contact VALUES('wxid_fixture');",
                        key_hex(&key)
                    ))
                    .unwrap();
            }
            let value = read_sqlcipher(&path, &key, |db| {
                let name: String = db
                    .query_row("SELECT username FROM contact", [], |row| row.get(0))
                    .map_err(|_| "fixture query failed".to_string())?;
                Ok(name)
            })
            .unwrap();
            assert_eq!(value, "wxid_fixture");
            let mut page = vec![0u8; 4096];
            let mut file = File::open(&path).unwrap();
            file.read_exact(&mut page).unwrap();
            assert!(verify_key(&key, &page));
            let _ = std::fs::remove_dir_all(&directory);
        }

        #[test]
        fn hex_runs_respect_bounds() {
            let mut value = b"x'".to_vec();
            value.extend(std::iter::repeat_n(b'a', 64));
            value.extend_from_slice(b"'");
            let runs = hex_runs(&value);
            assert_eq!(runs.len(), 1);
            assert_eq!(runs[0].len(), 64);
            let mut short = b"x'".to_vec();
            short.extend(std::iter::repeat_n(b'a', 63));
            short.push(b'\'');
            assert!(hex_runs(&short).is_empty());
        }
    }
}

#[cfg(not(windows))]
mod platform {
    use serde_json::{json, Value};

    pub struct Reader;

    impl Reader {
        pub fn open(_pid: u32, _created: Option<f64>) -> Result<Self, String> {
            Err("本机微信读取暂仅支持 Windows x64".to_string())
        }

        pub fn reader_id(&self) -> &str {
            ""
        }

        pub fn account(&self) -> &str {
            ""
        }

        pub fn verify(&mut self, _account: &str) -> Result<(), String> {
            Err("本机微信读取暂仅支持 Windows x64".to_string())
        }

        pub fn directory(&mut self) -> Result<Value, String> {
            Err("本机微信读取暂仅支持 Windows x64".to_string())
        }

        pub fn receive(&mut self, _peer: &str, _session: &Value) -> Result<Value, String> {
            Err("本机微信读取暂仅支持 Windows x64".to_string())
        }
    }

    pub fn random_bytes(length: usize) -> Vec<u8> {
        let mut value = vec![0u8; length];
        for (index, item) in value.iter_mut().enumerate() {
            *item = (index as u8).wrapping_mul(31).wrapping_add(17);
        }
        value
    }

    pub fn processes() -> Result<Value, String> {
        Err("本机微信读取暂仅支持 Windows x64".to_string())
    }
}

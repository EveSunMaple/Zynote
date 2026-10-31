/** 磁盘上使用英文目录名，界面统一展示中文。 */
export const libraryFolderLabels: Record<string, string> = {
 PPT: '课件',
 Transcripts: '语音原文',
 Notes: '笔记',
 PastExams: '历年卷',
 MediaDownloads: '媒体下载',
 Knowledge: '知识库',
 SubtitleSync: '字幕同步',
 NoteGeneration: '笔记生成',
 VisualOutputs: '画面识别',
 VideoTasks: '视频临时任务'
}

export const libraryCategoryLabels: Record<string, string> = {
 PPT: '课件',
 Transcripts: '语音原文',
 Notes: '笔记',
 PastExams: '历年卷'
}

/** 应用内部数据目录不进入文件管理器的目录树。 */
export const hiddenLibraryDirectories = new Set([
 'Knowledge',
 'SubtitleSync',
 'NoteGeneration',
 'VisualOutputs',
 'VideoTasks',
 '知识库数据',
 '字幕同步记录',
 '笔记生成记录',
 '画面识别成果',
 '视频提取临时任务'
])

export const hiddenLibraryPaths = new Set(['MediaDownloads/bili-session', 'MediaDownloads/download-auth'])

export function libraryFolderLabel(path: string) {
 if (!path) return ''
 if (!path.includes('/')) return libraryFolderLabels[path] ?? path
 return path.split('/').at(-1) ?? path
}

export function isHiddenLibraryEntry(entry: {type: string; path: string; name: string}) {
 if (entry.type !== 'dir') return false
 if (!entry.path.includes('/') && hiddenLibraryDirectories.has(entry.name)) return true
 return hiddenLibraryPaths.has(entry.path)
}

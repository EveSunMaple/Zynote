export type UpdatePhase =
 | 'disabled'
 | 'idle'
 | 'checking'
 | 'available'
 | 'downloading'
 | 'downloaded'
 | 'not-available'
 | 'error'

export type UpdateStatus = {
 phase: UpdatePhase
 currentVersion: string
 version?: string
 releaseDate?: string
 releaseNotes?: string
 percent?: number
 transferred?: number
 total?: number
 bytesPerSecond?: number
 message?: string
}

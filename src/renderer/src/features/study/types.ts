import type {Highlight} from '@shared/study'

export type Book = {id: string; path: string; pages: number}
export type AnalysisSource = 'teacher' | 'ai' | 'materials'
export type DraftPageWarning = {page: number; evidence: number; coordinates: number; invalid: number}
export type Draft = {
 source?: AnalysisSource
 id: string
 rows: Highlight[]
 warnings?: DraftPageWarning[]
 skippedContentsPages?: number[]
 analyzedRange?: [number, number]
}

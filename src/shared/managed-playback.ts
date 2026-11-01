import {z} from 'zod'
export const playbackID = z.string().regex(/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/)
export const playbackSHA = z.string().regex(/^[a-f0-9]{64}$/)
export const playbackOpenSchema = z
 .object({requestID: playbackID, videoTaskID: playbackID, expectedSourceSHA256: playbackSHA})
 .strict()
export const playbackCloseSchema = z.object({requestID: playbackID}).strict()
export const boundPlaybackOpenSchema = z
 .object({
  requestID: playbackID,
  summaryTaskID: playbackID,
  itemIndex: z.literal(0),
  expectedSourceBindingSHA256: playbackSHA
 })
 .strict()
export type BoundPlaybackOpen = z.infer<typeof boundPlaybackOpenSchema>
export const playbackDeclarationSchema = z
 .object({
  version: z.literal(1),
  verification: z.literal('declared'),
  taskID: playbackID,
  sourceKind: z.enum(['local', 'bili']),
  sourceIdentity: z.string().min(1).max(128),
  sourceSHA256: playbackSHA,
  size: z
   .number()
   .int()
   .positive()
   .max(8 * 1024 ** 3),
  bindingRevision: playbackSHA
 })
 .strict()
export type PlaybackDeclaration = z.infer<typeof playbackDeclarationSchema>
export const boundPlaybackDeclarationSchema = playbackDeclarationSchema
 .extend({sourceBindingSHA256: playbackSHA, bindingAuthoritySHA256: playbackSHA})
 .strict()
export type BoundPlaybackDeclaration = z.infer<typeof boundPlaybackDeclarationSchema>

export const playbackReleaseSchema = z
 .object({
  videoTaskID: playbackID,
  expectedRefKind: z.enum(['intent', 'receipt']),
  expectedRefSHA256: playbackSHA
 })
 .strict()
export const playbackReleasePlanSchema = z
 .object({
  version: z.literal(1),
  id: playbackID,
  refKind: z.enum(['intent', 'receipt']),
  refSHA256: playbackSHA,
  releaseID: playbackID,
  state: z.enum(['prepared', 'released']),
  files: z
   .array(
    z
     .object({
      name: z.string().min(1).max(128),
      size: z
       .number()
       .int()
       .nonnegative()
       .max(8 * 1024 ** 3),
      sha256: playbackSHA
     })
     .strict()
   )
   .max(32)
 })
 .strict()

export const playbackReleaseResultSchema = z
 .object({plan: playbackReleasePlanSchema, tombstoneSHA256: playbackSHA})
 .strict()

export const playbackReleasePrepareSchema = z.union([
 playbackReleaseResultSchema,
 z
  .object({
   status: z.literal('rejected'),
   mutation: z.literal('none'),
   code: z.enum(['INPUT_INVALID', 'NOT_FOUND', 'CONFLICT', 'BUSY', 'CANCELLED', 'TIMEOUT', 'UNAVAILABLE']),
   message: z.string().min(1).max(300)
  })
  .strict()
])

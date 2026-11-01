import {z} from 'zod'
export const playbackID = z.string().regex(/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/)
export const playbackSHA = z.string().regex(/^[a-f0-9]{64}$/)
export const playbackOpenSchema = z
 .object({requestID: playbackID, videoTaskID: playbackID, expectedSourceSHA256: playbackSHA})
 .strict()
export const playbackCloseSchema = z.object({requestID: playbackID}).strict()
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

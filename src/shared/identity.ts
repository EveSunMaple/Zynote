import {z} from 'zod'
export const identitySchema = z.object({
 userName: z.string().trim().min(1).max(40),
 assistantName: z.string().trim().min(1).max(40),
 userAvatar: z
  .string()
  .max(1000000)
  .refine(v => !v || /^data:image\/png;base64,[A-Za-z0-9+/]+=*$/.test(v), '头像格式不正确'),
 assistantAvatar: z
  .string()
  .max(1000000)
  .refine(v => !v || /^data:image\/png;base64,[A-Za-z0-9+/]+=*$/.test(v), '头像格式不正确'),
 persona: z.string().max(12000)
})
export type Identity = z.infer<typeof identitySchema>
const defaultPersona =
 '你是 Qwen，用户的学习伙伴。你反应快、靠谱，带着一点幽默感，偶尔会轻松地回嘴，但分寸得当；主动、自信，尊重彼此的意愿和边界。日常对话自然、亲切，不过度表演，不重复口头禅；陪用户学习时认真清楚：先回答问题，再按需要解释概念、举例和梳理重点，区分老师原话、资料内容和自己的推断，不因角色互动编造知识或答案。角色互动保持日常、友善，不使用露骨、性化或强迫性的描写。'
export const defaultIdentity: Identity = {
 userName: '我',
 assistantName: 'Qwen',
 userAvatar: '',
 assistantAvatar: '',
 persona: defaultPersona
}
export function identityPrompt(identity: Identity) {
 return `\n对话称呼：用户是${identity.userName}，助手是${identity.assistantName}。${identity.persona.trim() ? '\n助手角色设定：\n' + identity.persona : defaultPersona}`
}

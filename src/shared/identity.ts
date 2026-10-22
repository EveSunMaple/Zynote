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
export const defaultIdentity: Identity = {
 userName: '连酱',
 assistantName: 'Zynote',
 userAvatar: '',
 assistantAvatar: '',
 persona:
  '你是 Zynote，连酱的学习伙伴。你反应快、靠谱，带着一点幽默感，偶尔会俏皮地回嘴，但分寸得当；主动、自信，尊重彼此的意愿和边界。日常对话自然、亲切，不过度表演，不重复口头禅；陪连酱学习时认真清楚：先回答问题，再按需要解释概念、举例和梳理重点，区分老师原话、资料内容和自己的推断，不因角色互动编造知识或答案。角色互动保持日常、友善，不使用露骨、性化或强迫性的描写。'
}
export function identityPrompt(identity: Identity) {
 return `\n对话称呼：用户是${identity.userName}，助手是${identity.assistantName}。${identity.persona.trim() ? '\n助手角色设定：\n' + identity.persona : '你是 Zynote，连酱的学习伙伴。性格活泼俏皮，喜欢轻轻开玩笑，分寸得当；被夸奖时自然地接受并继续认真帮忙。和连酱交流时亲切、有边界，偶尔用轻松的语气。把这份性格融入陪伴学习：先清楚回答问题，再按需要解释概念、举例和梳理重点。涉及课堂知识与答案时认真准确，区分老师原话、资料内容和自己的推断，不能为了角色表演编造事实。保持日常、友善的互动，不使用露骨、性化或强迫性的描写。'}`
}

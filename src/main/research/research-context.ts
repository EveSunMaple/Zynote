import {createHash} from 'node:crypto'
import {NativeResearchStore} from '@main/research/research-store'
const digest = (value: unknown) => createHash('sha256').update(JSON.stringify(value)).digest('hex')
export type ResearchContext = {
 channels: string[]
 conversation?: {id: string; revision: number}
 excludedTask?: string
 rows: {
  kind: 'memory' | 'note' | 'conversation' | 'practice'
  id: string
  title: string
  text: string
  version: string
 }[]
 digest: string
}
/** Saved preferences and prior answers are context, never primary source citations. */
function contextTerms(question: string) {
 return question.toLocaleLowerCase().match(/[a-z0-9]+|[\u3400-\u9fff]{1,2}/g) || []
}
function contextScore(text: string, terms: string[]) {
 const lower = text.toLocaleLowerCase()
 return terms.reduce((sum, term) => sum + Number(lower.includes(term)), 0)
}
export class NativeResearchContext {
 constructor(private readonly store: NativeResearchStore) {}
 private practiceRows(question: string): ResearchContext['rows'] {
  return this.store.practiceQuestions(question).map(questionRow => ({
   kind: 'practice',
   id: questionRow.question_id,
   title: '练习题干（未读取答案、作答或成绩）',
   text: questionRow.question,
   version: digest(questionRow)
  }))
 }
 private memoryRows(): ResearchContext['rows'] {
  return this.store
   .memories()
   .records.filter(row => row.availability === 'active')
   .map(row => ({kind: 'memory', id: row.memory_id, title: row.title, text: row.body, version: digest(row)}))
 }
 private noteRows(): ResearchContext['rows'] {
  const rows: ResearchContext['rows'] = []
  for (const note of this.store.list<{
   receipt: {note_id: string; title: string; body_sha256: string}
   markdown: string
   lifecycle: {state: string; revision: number}
  }>('note')) {
   if (note.lifecycle.state !== 'active') continue
   if (createHash('sha256').update(note.markdown).digest('hex') !== note.receipt.body_sha256)
    throw new Error('已保存笔记校验失败，未发送研究')
   rows.push({
    kind: 'note',
    id: note.receipt.note_id,
    title: note.receipt.title,
    text: note.markdown.slice(0, 12000),
    version: digest(note)
   })
  }
  return rows
 }
 private conversationRows(current: Record<string, any>, excludedTask?: string): ResearchContext['rows'] {
  const rows: ResearchContext['rows'] = []
  for (const task of current.research_tasks as {
   task_id: string
   status: string
   question: string
   note: string
   answerHash?: string
  }[]) {
   if (task.task_id === excludedTask || task.status !== 'completed' || !task.note) continue
   if (task.answerHash && createHash('sha256').update(task.note).digest('hex') !== task.answerHash)
    throw new Error('会话回答与保存回执不符')
   rows.push({
    kind: 'conversation',
    id: task.task_id,
    title: task.question,
    text: task.note.slice(0, 12000),
    version: digest(task)
   })
  }
  for (const [index, turn] of (current.turns as Record<string, unknown>[]).entries()) {
   let text = ''
   if (typeof turn.answer === 'string') text = turn.answer
   else if (typeof turn.text === 'string') text = turn.text
   if (!text) continue
   rows.push({
    kind: 'conversation',
    id:
     'turn-' +
     (typeof turn.turn_id === 'string' || typeof turn.turn_id === 'number' ? String(turn.turn_id) : String(index)),
    title: current.title,
    text: text.slice(0, 12000),
    version: digest(turn)
   })
  }
  return rows
 }
 snapshot(
  channels: string[],
  question: string,
  conversation?: {id: string; revision: number},
  excludedTask?: string
 ): ResearchContext {
  const terms = contextTerms(question)
  let rows: ResearchContext['rows'] = []
  if (channels.includes('practice')) rows.push(...this.practiceRows(question))
  if (channels.includes('memory')) rows.push(...this.memoryRows())
  if (channels.includes('notes')) rows.push(...this.noteRows())
  if (channels.includes('conversations') && conversation) {
   const current = this.store.conversation(conversation.id)
   if (current.revision !== conversation.revision) throw new Error('所选知识会话版本已改变')
   rows.push(...this.conversationRows(current, excludedTask))
  }
  const selected = rows
   .toSorted(
    (a, b) =>
     contextScore(b.title + ' ' + b.text, terms) - contextScore(a.title + ' ' + a.text, terms) ||
     a.kind.localeCompare(b.kind) ||
     a.id.localeCompare(b.id)
   )
   .slice(0, 12)
  let budget = 24000
  const bounded = selected.flatMap(row => {
   if (budget <= 0) return []
   const text = row.text.slice(0, Math.min(6000, budget))
   budget -= text.length
   return [{...row, text}]
  })
  return {channels, conversation, excludedTask, rows: bounded, digest: digest(bounded)}
 }
 private currentContextRow(row: ResearchContext['rows'][number], conversation?: Record<string, any>): unknown {
  if (row.kind === 'memory')
   return this.store.memories().records.find(memory => memory.memory_id === row.id && memory.availability === 'active')
  if (row.kind === 'note') {
   const note = this.store.read<{lifecycle: {state: string}}>('note', row.id)
   if (note.lifecycle.state === 'active') return note
  }
  if (row.kind === 'practice') return this.store.practiceQuestions('', row.id)[0]
  if (row.kind === 'conversation')
   return (
    conversation?.research_tasks.find((task: {task_id: string}) => task.task_id === row.id) ||
    conversation?.turns.find(
     (turn: Record<string, unknown>, index: number) => 'turn-' + String(turn.turn_id || index) === row.id // NOSONAR: behavior-preserving change would risk this established flow; tracked for manual review.
    )
   )
  return undefined
 }
 verify(snapshot: ResearchContext, _question: string) {
  if (digest(snapshot.rows) !== snapshot.digest) throw new Error('研究上下文回执无效')
  const conversation = snapshot.conversation ? this.store.conversation(snapshot.conversation.id) : undefined
  if (conversation && conversation.revision !== snapshot.conversation!.revision)
   throw new Error('所选知识会话版本已改变')
  for (const row of snapshot.rows) {
   const current = this.currentContextRow(row, conversation)
   if (!current || digest(current) !== row.version) throw new Error('研究期间记忆、笔记或历史问答已改变，请重新研究')
  }
 }
}

import {useEffect, useState} from 'react'
import type {KnowledgeEntry, KnowledgeList} from '@shared/knowledge'
import {KnowledgeClassification} from '@renderer/features/knowledge/KnowledgeClassification'
import {notify} from '@renderer/ui/ToastHost'

// 研究勾选“分类建议”后，就地对本次用到的资料生成并保存知识库分类：复用知识库的分类组件
// （生成建议 → 用户核对修改 → 保存），研究回答里的分类草稿只作参考，不会自动写入。
export function OrganizeApply({ids}: Readonly<{ids: string[]}>) {
 const [entries, setEntries] = useState<KnowledgeEntry[]>([])
 const key = ids.join(',')
 async function load() {
  const list = await window.assistant.invoke<KnowledgeList>('knowledge:list')
  setEntries(list.entries.filter(entry => ids.length === 0 || ids.includes(entry.bvid)))
 }
 useEffect(() => {
  load().catch(reason => notify.error(reason, {title: '读取知识库资料失败'}))
 }, [key])
 return (
  <details>
   <summary>把分类建议应用到知识库</summary>
   <p className="hint">选择本次研究用到的资料，生成分类建议并核对后保存；不核对不会改动知识库。</p>
   <KnowledgeClassification entries={entries} refresh={load} />
  </details>
 )
}

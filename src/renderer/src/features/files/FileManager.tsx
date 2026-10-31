import {Button, Input, Tabs, ToggleButton, ToggleButtonGroup} from '@heroui/react'
import {useEffect, useState} from 'react'
import type {LibraryEntry, LibraryFile} from '@shared/types'
import {Folder, Trash2} from 'lucide-react'
import {notify} from '@renderer/ui/ToastHost'
import {WindowPanel} from '@renderer/ui/WindowPanel'
import {ConfirmDialog} from '@renderer/ui/ConfirmDialog'
import {MediaDownloads} from '@renderer/features/bilibili/MediaDownloads'
import {LibraryTree} from '@renderer/features/files/LibraryTree'
const categories = ['全部', 'PPT', 'Transcripts', 'Notes', 'PastExams']
const openable = /\.(pdf|pptx?|docx?|txt|md|srt)$/i
export function FileManager({
 busy,
 close,
 changed
}: Readonly<{
 busy: boolean
 close: () => void
 changed: () => Promise<unknown>
}>) {
 const [files, setFiles] = useState<LibraryFile[]>([]),
  [filter, setFilter] = useState('全部'),
  [search, setSearch] = useState(''),
  [pending, setPending] = useState<LibraryFile | null>(null),
  [working, setWorking] = useState(false),
  [source, setSource] = useState<'course' | 'media'>('course'),
  [directory, setDirectory] = useState(''),
  [treeRefresh, setTreeRefresh] = useState(0)
 async function load() {
  let lastError: unknown
  for (let attempt = 0; attempt < 3; attempt++) {
   try {
    setFiles(await window.assistant.invoke<LibraryFile[]>('library:list'))
    return
   } catch (reason) {
    lastError = reason
    if (attempt < 2) await new Promise(resolve => setTimeout(resolve, 300 * (attempt + 1)))
   }
  }
  throw lastError
 }
 async function refresh() {
  setTreeRefresh(value => value + 1)
  await load()
 }
 useEffect(() => {
  void load().catch(e => notify.error(e, {title: '读取文件失败'}))
 }, [])
 async function openEntry(entry: LibraryEntry) {
  setDirectory(entry.path.includes('/') ? entry.path.slice(0, entry.path.lastIndexOf('/')) : '')
  setFilter('全部')
  setSearch('')
  try {
   if (openable.test(entry.name)) await window.assistant.invoke('library:open-file', {path: entry.path})
   else await window.assistant.invoke('library:reveal', {path: entry.path})
  } catch (e) {
   notify.error(e, {title: '打开文件失败'})
  }
 }
 async function remove() {
  if (!pending) return
  setWorking(true)
  try {
   await window.assistant.invoke('library:delete', {path: pending.path, confirmed: true})
   setPending(null)
   await refresh()
   await changed()
   notify.success('文件已移入回收站')
  } catch (e) {
   notify.error(e, {title: '删除文件失败'})
  } finally {
   setWorking(false)
  }
 }
 const selectedFiles = files.filter(f => !directory || f.path.startsWith(directory + '/'))
 const visible = selectedFiles.filter(
  f => (filter === '全部' || f.category === filter) && `${f.name} ${f.course}`.includes(search)
 )
 const directoryName = directory ? directory.split('/').at(-1) || directory : '全部文件'
 return (
  <WindowPanel
   visible
   label="文件管理"
   title="文件管理"
   description="按工作目录浏览资料；文件不会自动进入知识库。"
   heading="文件管理"
   subheading={source === 'course' ? `${directoryName} · ${visible.length} 个文件` : '媒体下载任务与已下载文件'}
   menu={
    <LibraryTree
     selected={directory}
     onSelect={path => {
      setDirectory(path)
      setFilter('全部')
      setSearch('')
     }}
     onOpen={entry => void openEntry(entry)}
     onRefresh={() => void refresh().catch(e => notify.error(e, {title: '读取文件失败'}))}
     refreshKey={treeRefresh}
    />
   }
   actions={
    source === 'course' ? (
     <>
      <Button
       variant="ghost"
       size="sm"
       onClick={() => void refresh().catch(e => notify.error(e, {title: '读取文件失败'}))}
      >
       刷新
      </Button>
      <Button variant="tertiary" size="sm" onClick={() => void window.assistant.invoke('library:open')}>
       打开资料文件夹
      </Button>
     </>
    ) : null
   }
   close={close}
  >
   <div className="panel-section">
    <Tabs
     className="file-source-tabs"
     selectedKey={source}
     onSelectionChange={key => {
      if (key === 'course' || key === 'media') setSource(key)
     }}
    >
     <Tabs.ListContainer className="app-tabs__list-container">
      <Tabs.List aria-label="文件来源">
       <Tabs.Tab className="app-tabs__tab" id="course">
        课程资料
        <Tabs.Indicator />
       </Tabs.Tab>
       <Tabs.Tab className="app-tabs__tab" id="media">
        媒体下载
        <Tabs.Indicator />
       </Tabs.Tab>
      </Tabs.List>
     </Tabs.ListContainer>
     <Tabs.Panel id="course">
      <div className="file-source-panel">
       <div className="file-management-tools">
        <Input
         aria-label="搜索资料文件"
         placeholder="搜索文件或课程…"
         value={search}
         onChange={e => setSearch(e.target.value)}
        />
       </div>
       <ToggleButtonGroup
        aria-label="文件分类"
        selectionMode="single"
        disallowEmptySelection
        selectedKeys={new Set([filter])}
        onSelectionChange={keys => {
         const next = [...keys][0]
         if (typeof next === 'string') setFilter(next)
        }}
        size="sm"
        className="file-category-group"
       >
        {categories.map((c, index) => (
         <ToggleButton key={c} id={c}>
          {index > 0 && <ToggleButtonGroup.Separator />}
          {c} {selectedFiles.filter(f => c === '全部' || f.category === c).length}
         </ToggleButton>
        ))}
       </ToggleButtonGroup>
       <div className="file-management-list">
        {visible.map(f => (
         <article key={f.path}>
          <span className="file-type-icon">
           <Folder />
          </span>
          <div>
           <strong title={f.name}>{f.name}</strong>
           <small>
            {f.category} · {f.course || '未分类'} · {(f.size / 1024 / 1024).toFixed(2)} MB
           </small>
          </div>
          {openable.test(f.name) && (
           <Button
            variant="tertiary"
            size="sm"
            aria-label={`打开文件 ${f.name}`}
            onClick={() =>
             void window.assistant
              .invoke('library:open-file', {path: f.path})
              .catch(e => notify.error(e, {title: '打开文件失败'}))
            }
           >
            {/\.pdf$/i.test(f.name) ? '打开 PDF' : '打开'}
           </Button>
          )}
          <Button
           variant="tertiary"
           size="sm"
           onClick={() =>
            void window.assistant
             .invoke('library:reveal', {path: f.path})
             .catch(e => notify.error(e, {title: '定位文件失败'}))
           }
          >
           定位
          </Button>
          <Button
           variant="danger-soft"
           size="sm"
           isIconOnly
           aria-label={`删除文件 ${f.name}`}
           isDisabled={busy || working}
           onClick={() => setPending(f)}
          >
           <Trash2 />
          </Button>
         </article>
        ))}
        {!visible.length && (
         <div className="empty">
          <h3>{directory ? '此目录没有课程资料' : '这里还没有文件'}</h3>
          <p>
           {directory ? '可直接在左侧目录打开文件；下载文件请在“媒体下载”页签查看。' : '导入或导出资料后会显示在这里。'}
          </p>
         </div>
        )}
       </div>
      </div>
     </Tabs.Panel>
     <Tabs.Panel id="media">
      <MediaDownloads visible />
     </Tabs.Panel>
    </Tabs>
    {pending && (
     <ConfirmDialog
      title="将文件移入回收站？"
      confirmLabel="移入回收站"
      busy={working}
      onCancel={() => setPending(null)}
      onConfirm={() => void remove()}
     >
      <p>{pending.name}</p>
      <p>相关课次会解除此文件的关联；已经生成的字幕和笔记保留。</p>
     </ConfirmDialog>
    )}
   </div>
  </WindowPanel>
 )
}

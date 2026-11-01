import {Button, Tabs} from '@heroui/react'
import {useState} from 'react'
import {Modal} from '@renderer/ui/UiParts'
import academicGrid from '@renderer/assets/schedule-examples/academic-grid.png'
import mobileColor from '@renderer/assets/schedule-examples/mobile-color.jpg'
import mobileBlue from '@renderer/assets/schedule-examples/mobile-blue.jpg'

const examples = [
 {label: '教务系统', alt: '教务系统单双周课表', src: academicGrid},
 {label: '手机彩色', alt: '手机彩色课程块课表', src: mobileColor},
 {label: '手机单色', alt: '手机单色课程块课表', src: mobileBlue}
]

export function ScheduleExamples() {
 const [open, setOpen] = useState(false)
 const [zoomed, setZoomed] = useState(false)
 return (
  <>
   <Button size="sm" variant="ghost" aria-haspopup="dialog" onPress={() => setOpen(true)}>
    查看示例
   </Button>
   {open && (
    <Modal
     title="课表截图示例"
     close={() => {
      setOpen(false)
      setZoomed(false)
     }}
    >
     <p className="schedule-examples__hint">截图时保留星期、节次和课程名称。点击图片可切换原尺寸。</p>
     <Tabs className="schedule-examples" onSelectionChange={() => setZoomed(false)}>
      <Tabs.ListContainer className="app-tabs__list-container">
       <Tabs.List aria-label="示例排版">
        {examples.map(({label}) => (
         <Tabs.Tab key={label} className="app-tabs__tab" id={label}>
          {label}
          <Tabs.Indicator />
         </Tabs.Tab>
        ))}
       </Tabs.List>
      </Tabs.ListContainer>
      {examples.map(({label, alt, src}) => (
       <Tabs.Panel key={label} id={label}>
        <div className="schedule-examples__frame">
         <button
          className="schedule-examples__zoom"
          type="button"
          aria-label={'原尺寸显示' + alt}
          aria-pressed={zoomed}
          onClick={() => setZoomed(value => !value)}
         >
          <img className="schedule-examples__image" src={src} alt={alt} />
         </button>
        </div>
       </Tabs.Panel>
      ))}
     </Tabs>
    </Modal>
   )}
  </>
 )
}

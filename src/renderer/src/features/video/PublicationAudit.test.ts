import {createElement} from 'react'
import {renderToStaticMarkup} from 'react-dom/server'
import {expect, it} from 'vitest'
import {PublicationAudit} from './PublicationAudit'

it('renders a saved declaration without claiming current verification or auto invoking IPC', () => {
 const markup = renderToStaticMarkup(
  createElement(PublicationAudit, {
   target: {
    id: 'task',
    itemIndex: 0,
    record: {
     state: 'recorded',
     planSHA256: 'a'.repeat(64),
     receiptSHA256: 'b'.repeat(64),
     mdPositionScope: 'anchor-appendix'
    }
   }
  })
 )
 expect(markup).toContain('保存时已有成果凭据')
 expect(markup).toContain('核验成果引用')
 expect(markup).not.toContain('核验通过')
 expect(markup).toContain('不持续监控')
})

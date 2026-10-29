import {execFileSync, spawnSync} from 'node:child_process'
import {mkdir, mkdtemp, readdir, rm, writeFile} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join, resolve} from 'node:path'
import {_electron, type ElectronApplication} from 'playwright'

const appIndex = process.argv.indexOf('--app')
if (appIndex < 0 || !process.argv[appIndex + 1])
 throw new Error('Usage: node scripts/migration-drill.mts --app <packaged executable>')
if (spawnSync('sqlite3', ['--version']).status !== 0) throw new Error('sqlite3 CLI is required for the migration drill')
const executablePath = resolve(process.argv[appIndex + 1])
const root = await mkdtemp(join(tmpdir(), 'zynote-migration-drill-'))
const legacy = join(root, '资料')
const library = join(root, 'Library')
const database = join(root, 'assistant.db')
let application: ElectronApplication | undefined
function sql(lines: string, db = database) {
 execFileSync('sqlite3', [db], {input: lines})
}
function query(statement: string, db = database) {
 return execFileSync('sqlite3', [db, statement], {encoding: 'utf8'}).trim()
}
try {
 await mkdir(join(legacy, 'PPT'), {recursive: true})
 await writeFile(join(legacy, 'PPT', 'old.pdf'), 'pdf')
 const oldPath = join(legacy, 'PPT', 'old.pdf')
 const insert = (value: string) => value.replaceAll("'", "''")
 sql(`CREATE TABLE courses(id TEXT PRIMARY KEY,name TEXT NOT NULL,created_at TEXT DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE course_files(id TEXT PRIMARY KEY,course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,kind TEXT NOT NULL,filename TEXT,path TEXT,text TEXT,chunks_json TEXT,needs_ocr INTEGER DEFAULT 0);
CREATE TABLE lectures(id TEXT PRIMARY KEY,course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,title TEXT,video_path TEXT,ppt_path TEXT,ppt_text TEXT,status TEXT DEFAULT '待转写',created_at TEXT DEFAULT CURRENT_TIMESTAMP);
INSERT INTO courses(id,name) VALUES('00000000-0000-4000-8000-000000000001','课程');
INSERT INTO course_files(id,course_id,kind,path) VALUES('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000001','past_exam','${insert(oldPath)}');
INSERT INTO lectures(id,course_id,title,ppt_path) VALUES('00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000001','课次','${insert(oldPath)}');
PRAGMA user_version = 0;`)
 application = await _electron.launch({
  executablePath,
  env: {...process.env, LISTENING_ASSISTANT_DATA: root}
 })
 const page = await application.firstWindow({timeout: 45000})
 await page.waitForLoadState('domcontentloaded')
 const info = await page.evaluate(() =>
  (window as typeof window & {assistant: {invoke: (action: string) => Promise<unknown>}}).assistant.invoke('app:info')
 )
 if (!info || typeof info !== 'object') throw new Error('packaged app did not answer app:info')
 if (query('PRAGMA user_version') !== '1') throw new Error('schema version was not upgraded')
 const migrated = join(library, 'PPT', 'old.pdf')
 if (query("SELECT path FROM course_files WHERE id='00000000-0000-4000-8000-000000000002'") !== migrated)
  throw new Error('course file path was not migrated')
 if (query("SELECT ppt_path FROM lectures WHERE id='00000000-0000-4000-8000-000000000003'") !== migrated)
  throw new Error('lecture path was not migrated')
 const backups = await readdir(join(root, 'Backups'))
 if (backups.length !== 1) throw new Error('expected exactly one backup')
 const backup = join(root, 'Backups', backups[0])
 if (query('PRAGMA user_version', backup) !== '0') throw new Error('backup lost old schema version')
 if (query("SELECT path FROM course_files WHERE id='00000000-0000-4000-8000-000000000002'", backup) !== oldPath)
  throw new Error('backup lost old path')
 console.log(JSON.stringify({status: 'passed', backup: backups[0], schemaVersion: 1}))
} finally {
 await application?.close().catch(() => {})
 await rm(root, {recursive: true, force: true})
}

import test from 'node:test'
import assert from 'node:assert/strict'
import {inputRows} from '../src/sdk/task-inputs.ts'
import {WorkbenchClient} from '../src/sdk/client.ts'

test('complete long input and business requirements retain text, zero limits and model snapshot',()=>{
 const long='完整中文要求\n'.repeat(500)
 const rows=new Map(inputRows({template:'wechat_crm',mode:'followup',goal:long,body:long,business:{prompt:long,knowledge:long,targets:['客户甲','客户乙'],max_replies:0,add_friends:false},model_snapshot:{name:'创建时的模型',provider:'codex_cli',image_max_width:-1}}))
 assert.equal(rows.get('任务目标'),long);assert.equal(rows.get('正文'),long);assert.equal(rows.get('产品知识'),long);assert.equal(rows.get('最多发送回复'),'0');assert.equal(rows.get('指定对象'),'客户甲\n客户乙');assert.equal(rows.get('创建时模型配置'),'创建时的模型');assert.equal(rows.get('模型截图宽度'),'原图')
})
test('archive task loading traverses full cursor pages',async()=>{
 const c=new WorkbenchClient('http://test'),paths=[]
 c.request=async path=>{paths.push(path);return paths.length===1?Array.from({length:100},(_,i)=>({id:'id'+i})):[{id:'older'}]}
 const tasks=await c.taskList(true)
 assert.equal(tasks.length,101);assert.equal(paths[0],'/tasks?archived=true');assert.equal(paths[1],'/tasks?archived=true&before=id99')
})

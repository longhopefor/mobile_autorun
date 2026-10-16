import assert from 'node:assert/strict'
import test from 'node:test'
import {buildReplay,replayHTML} from '../src/sdk/replay.ts'
import {WorkbenchClient} from '../src/sdk/client.ts'

const event=(id,type,data)=>({id,type,data,created_at:'2026-10-12T00:00:00Z'})
const before={id:'before',asset_id:'a',width:1080,height:2400}
const after={id:'after',asset_id:'b',width:1080,height:2400}
const proposal=event(2,'model_decision',{decision:{observation_id:'before',tool:'phone_action',action:{kind:'tap',x:500,y:600},reason:'点击搜索'},duration_ms:1200,input:{width:960}})

test('pairs original decision and explicit after evidence, retaining model latency',()=>{
 const frames=buildReplay([event(1,'observation',before),proposal,event(3,'action_reported',{action_id:'cmd',observation_id:'before',action:{kind:'tap',x:500,y:600}}),event(4,'observation',after),event(5,'action_result',{action_id:'cmd',observation_id:'before',after_observation_id:'after'})])
 assert.equal(frames.length,1);assert.equal(frames[0].before.asset_id,'a');assert.equal(frames[0].after.asset_id,'b');assert.equal(frames[0].durationMS,1200);assert.equal(frames[0].afterLabel,'执行后截图')
})
test('discarded, stopped and uncertain actions never claim execution success',()=>{
 for(const [type,data,status] of [
  ['screen_reobserve',{discarded_observation_id:'before'},'页面变化，动作已丢弃'],
  ['no_progress_paused',{observation_id:'before'},'重复无进展，动作未下发'],
  ['action_uncertain',{action_id:'cmd',observation_id:'before'},'执行结果不确定，需核对']
 ]){
  const [frame]=buildReplay([event(1,'observation',before),proposal,event(3,type,data)])
  assert.equal(frame.status,status);assert.equal(frame.after,undefined)
 }
})
test('legacy steps label subsequent observation honestly',()=>{
 const [frame]=buildReplay([event(1,'observation',before),proposal,event(3,'action_reported',{observation_id:'before'}),event(4,'observation',after)])
 assert.equal(frame.afterLabel,'后续观察（历史记录）')
})
test('HTML export embeds images, marks intent and escapes untrusted screen text',()=>{
 const [frame]=buildReplay([event(1,'observation',before),proposal])
 frame.reason='<script>alert(1)</script>';frame.action={kind:'type',text:'<img src=x onerror=alert(1)>'}
 const html=replayHTML('task', [frame],new Map([['a','data:image/png;base64,fixture']]))
 assert.ok(html.includes('data:image/png;base64,fixture'));assert.ok(html.includes('&lt;script&gt;'));assert.ok(!html.includes('<script>'));assert.ok(!html.includes('<img src=x'))
})
test('task step loading traverses all 100-row pages',async()=>{
 const c=new WorkbenchClient('http://test'),paths=[]
 c.request=async path=>{paths.push(path);if(paths.length===1)return Array.from({length:100},(_,i)=>event(i+1,'status',{}));return [event(101,'status',{})]}
 const events=await c.taskSteps('task')
 assert.equal(events.length,101);assert.equal(paths[1],'/tasks/task/steps?after=100')
})

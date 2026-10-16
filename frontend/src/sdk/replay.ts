import type {Observation, Step} from './client'

export interface ReplayFrame {
 id:number
 at:string
 observationID:string
 before?:Observation
 after?:Observation
 afterLabel?:string
 action?:Record<string,any>
 reason:string
 status:string
 durationMS?:number
 input?:Record<string,number>
 usage?:Record<string,any>
}

// Join by observation/action IDs, never by the index of a model call. A
// discarded proposal must not appear as an executed tap in the replay.
export function buildReplay(events:Step[]):ReplayFrame[]{
 const sorted=[...events].sort((a,b)=>a.id-b.id)
 const observations=new Map<string,Observation>()
 for(const e of sorted)if(e.type==='observation'&&e.data.id)observations.set(e.data.id,e.data as Observation)
 const frames:ReplayFrame[]=[]
 for(const e of sorted){
  if(!['model_decision','rule_decision'].includes(e.type)||!e.data.decision?.observation_id)continue
  const d=e.data.decision, observationID=d.observation_id
  const frame:ReplayFrame={id:e.id,at:e.created_at,observationID,before:observations.get(observationID),action:d.action||undefined,reason:d.reason||'',status:e.data.error?'决策失败':d.tool==='finish_task'?'完成候选，待核验':d.tool==='request_takeover'?'请求接管':d.tool==='phone_action'?'未确认下发':'业务决策',durationMS:e.type==='model_decision'?e.data.duration_ms:undefined,input:e.data.input,usage:e.data.usage}
  // Limit this join to the next decision: repeated proposals may reuse an ID
  // in legacy test data, and manual operations are not this model's action.
  const next=sorted.find(n=>n.id>e.id&&['model_decision','rule_decision'].includes(n.type))?.id??Infinity
  const following=sorted.filter(n=>n.id>e.id&&n.id<next)
  const outcome=following.find(n=>['action_reported','action_uncertain'].includes(n.type)&&n.data.observation_id===observationID)
  if(outcome){
   frame.action=outcome.data.action||frame.action
   frame.status=outcome.type==='action_uncertain'?'执行结果不确定，需核对':'动作有回报，待核验'
   const result=following.find(n=>n.type==='action_result'&&n.data.action_id===outcome.data.action_id)
   if(result){frame.after=observations.get(result.data.after_observation_id);frame.afterLabel='执行后截图'}
   else if(outcome.type==='action_reported'){
    const later=following.find(n=>n.type==='observation'&&n.id>outcome.id)
    if(later){frame.after=later.data as Observation;frame.afterLabel='后续观察（历史记录）'}
   }
  }
  if(following.some(n=>['screen_reobserve','screen_reobserve_exhausted'].includes(n.type)&&n.data.discarded_observation_id===observationID))frame.status='页面变化，动作已丢弃'
  if(following.some(n=>n.type==='no_progress_paused'&&n.data.observation_id===observationID))frame.status='重复无进展，动作未下发'
  if(following.some(n=>n.type==='no_progress_warning'&&n.data.observation_id===observationID))frame.status+=' · 重复无进展提醒'
  frames.push(frame)
 }
 return frames
}

export function actionLabel(a?:Record<string,any>):string{
 if(!a)return '观察与判断'
 const names:Record<string,string>={tap:'点击',swipe:'滑动',type:'输入文字',launch:'启动应用',back:'返回',home:'回桌面',wait:'等待'}
 const target=a.target?.text||a.target?.description||a.target?.resource_id
 if(a.kind==='type')return `输入：${a.text||''}`
 if(a.kind==='launch')return `启动：${a.package||''}`
 if(a.kind==='swipe'){
  const dx=point(a.end_x)-point(a.x),dy=point(a.end_y)-point(a.y)
  return Math.abs(dy)>=Math.abs(dx)?(dy<0?'向上滑动':'向下滑动'):(dx<0?'向左滑动':'向右滑动')
 }
 return (names[a.kind]||a.kind)+(target?` · ${target}`:'')
}

const escapeHTML=(s:unknown)=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]!))
export function replayHTML(taskID:string,frames:ReplayFrame[],images:Map<string,string>):string{
 const picture=(o:Observation|undefined,a:Record<string,any>|undefined)=>{
  const src=o&&images.get(o.asset_id)
  if(!src)return '<p>没有截图证据</p>'
  let mark=''
  if(a?.kind==='tap')mark=`<circle cx="${point(a.x)}" cy="${point(a.y)}" r="18"/>`
  if(a?.kind==='swipe')mark=`<path d="M ${point(a.x)} ${point(a.y)} L ${point(a.end_x)} ${point(a.end_y)}"/><circle cx="${point(a.end_x)}" cy="${point(a.end_y)}" r="12"/>`
  return `<div class="picture"><img src="${escapeHTML(src)}" alt="任务截图"/><svg viewBox="0 0 1000 1000" preserveAspectRatio="none">${mark}</svg></div>`
 }
 const cards=frames.map((f,i)=>`<article><h2>第 ${i+1} 步 · ${escapeHTML(actionLabel(f.action))}</h2><p>${escapeHTML(f.status)} · ${escapeHTML(f.at)}${f.durationMS!==undefined?` · 模型 ${(f.durationMS/1000).toFixed(2)} 秒`:''}</p><p>${escapeHTML(f.reason)}</p><div class="pair"><section><h3>决策时截图 · 动作位置</h3>${picture(f.before,f.action)}</section><section><h3>${escapeHTML(f.afterLabel||'执行后截图')}</h3>${picture(f.after,undefined)}</section></div><details><summary>动作、输入尺寸与用量</summary><pre>${escapeHTML(JSON.stringify({action:f.action,input:f.input,usage:f.usage},null,2))}</pre></details></article>`).join('')
 return `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>任务回放 ${escapeHTML(taskID)}</title><style>body{font:14px system-ui;margin:24px;color:#25324a;background:#f3f5fa}article{background:white;padding:20px;margin:20px 0;border-radius:12px}.pair{display:flex;gap:24px;flex-wrap:wrap}section{width:320px}.picture{position:relative;width:100%}.picture img{width:100%;display:block}.picture svg{position:absolute;inset:0;width:100%;height:100%;pointer-events:none;fill:none;stroke:#ff542e;stroke-width:5}p,pre,h2{overflow-wrap:anywhere}pre{white-space:pre-wrap}</style><h1>任务动作回放</h1><p>${escapeHTML(taskID)} · 标记表示动作意图；动作回报不等于业务成功。</p>${cards||'<p>暂无决策记录</p>'}</html>`
}
export function point(n:unknown):number{const v=Number(n);return Number.isFinite(v)?Math.max(0,Math.min(1000,v)):0}

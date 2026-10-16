<script setup lang="ts">
import {computed,ref,watch} from 'vue'
import {client,type Step} from '../sdk/client'
import {actionLabel,buildReplay,point,replayHTML} from '../sdk/replay'
const props=defineProps<{taskId:string;events:Step[]}>()
const frames=computed(()=>buildReplay(props.events))
const index=ref(0),exporting=ref(false),error=ref('')
const frame=computed(()=>frames.value[index.value])
const totalMS=computed(()=>frames.value.reduce((n,f)=>n+(f.durationMS||0),0))
watch(()=>props.taskId,()=>{index.value=0;error.value=''})
watch(()=>frames.value.length,n=>{index.value=Math.min(index.value,Math.max(0,n-1))})
async function exportReplay(){
 exporting.value=true;error.value=''
 const taskId=props.taskId,snapshot=[...frames.value]
 try{
  const images=new Map<string,string>()
  const ids=new Set(snapshot.flatMap(f=>[f.before?.asset_id,f.after?.asset_id]).filter((id):id is string=>!!id))
  let bytes=0
  for(const id of ids){
   const response=await fetch(client.assetURL(id),{credentials:'include'})
   if(!response.ok)throw new Error('截图读取失败，请重新登录或刷新')
   const blob=await response.blob();bytes+=blob.size
   if(bytes>64*1024*1024)throw new Error('截图总量超过 64 MB，请选择较短任务导出')
   if(blob.type!=='image/png'&&blob.type!=='image/jpeg')throw new Error('截图格式无效')
   const data=await new Promise<string>((resolve,reject)=>{const r=new FileReader();r.onload=()=>resolve(String(r.result));r.onerror=()=>reject(new Error('截图编码失败'));r.readAsDataURL(blob)})
   images.set(id,data)
  }
  const url=URL.createObjectURL(new Blob([replayHTML(taskId,snapshot,images)],{type:'text/html;charset=utf-8'}))
  const a=document.createElement('a');a.href=url;a.download=`task-replay-${taskId}.html`;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)
 }catch(e){error.value=(e as Error).message}finally{exporting.value=false}
}
</script>
<template>
 <section class="task-replay">
  <div class="replay-header"><div><h3>动作回放</h3><small>{{frames.length}} 次决策 · 模型累计 {{(totalMS/1000).toFixed(2)}} 秒</small></div><el-button size="small" :disabled="!frames.length" :loading="exporting" @click="exportReplay">导出完整 HTML</el-button></div>
  <p v-if="error" class="error">{{error}}</p>
  <template v-if="frame">
   <div class="replay-controls"><el-button size="small" :disabled="index===0" @click="index--">上一步</el-button><span>{{index+1}} / {{frames.length}}</span><el-button size="small" :disabled="index>=frames.length-1" @click="index++">下一步</el-button></div>
   <p class="replay-action">{{actionLabel(frame.action)}}</p><p>{{frame.reason}}</p><el-tag>{{frame.status}}</el-tag>
   <p class="subtle">{{new Date(frame.at).toLocaleString()}}<template v-if="frame.durationMS!==undefined"> · 模型 {{(frame.durationMS/1000).toFixed(2)}} 秒</template></p>
   <p v-if="frame.input" class="subtle">模型图 {{frame.input.width}}×{{frame.input.height}}（原图 {{frame.input.original_width}}×{{frame.input.original_height}}） · {{Math.round(frame.input.bytes/1024)}} KB / 原图 {{Math.round(frame.input.original_bytes/1024)}} KB</p>
   <div class="replay-pair">
    <div><h4>决策时截图 · 动作位置</h4><div v-if="frame.before" class="replay-picture"><img :src="client.assetURL(frame.before.asset_id)" alt="决策时截图"/><svg viewBox="0 0 1000 1000" preserveAspectRatio="none" aria-label="动作位置"><circle v-if="frame.action?.kind==='tap'" :cx="point(frame.action.x)" :cy="point(frame.action.y)" r="18"/><template v-if="frame.action?.kind==='swipe'"><path :d="`M ${point(frame.action.x)} ${point(frame.action.y)} L ${point(frame.action.end_x)} ${point(frame.action.end_y)}`"/><circle :cx="point(frame.action.end_x)" :cy="point(frame.action.end_y)" r="12"/></template></svg></div><p v-else class="subtle">没有截图证据</p></div>
    <div><h4>{{frame.afterLabel||'执行后截图'}}</h4><img v-if="frame.after" class="replay-after" :src="client.assetURL(frame.after.asset_id)" alt="执行后截图"/><p v-else class="subtle">尚无执行后截图；请以动作状态判断是否已执行。</p></div>
   </div>
   <details><summary>动作与模型用量</summary><pre>{{JSON.stringify({action:frame.action,usage:frame.usage},null,2)}}</pre></details>
  </template><p v-else class="subtle">暂无决策记录</p>
 </section>
</template>
<style scoped>
.task-replay{margin:24px 0;border-top:1px solid #e6eaf2;padding-top:20px}.replay-header{display:flex;justify-content:space-between;gap:12px;align-items:center}.replay-header h3{margin:0 0 6px}.replay-header small{color:#748197}.replay-controls{display:flex;align-items:center;gap:14px;margin-top:18px}.replay-action{font-weight:600;overflow-wrap:anywhere}.replay-pair{display:grid;grid-template-columns:1fr 1fr;gap:18px;margin:16px 0}.replay-pair>div{min-width:0}.replay-pair h4{font-size:12px;color:#748197}.replay-picture{position:relative}.replay-picture img,.replay-after{display:block;width:100%;border-radius:8px}.replay-picture svg{position:absolute;inset:0;width:100%;height:100%;pointer-events:none;fill:none;stroke:#ff542e;stroke-width:5}.task-replay p,.task-replay pre{overflow-wrap:anywhere}.task-replay pre{white-space:pre-wrap;font-size:12px}@media(max-width:760px){.replay-pair{grid-template-columns:1fr}.replay-header{align-items:flex-start}}
</style>

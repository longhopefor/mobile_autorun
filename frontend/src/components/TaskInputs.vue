<script setup lang="ts">
import {computed,onMounted,ref,watch} from 'vue'
import {client,type Asset,type Peer,type Profile,type Task,type TaskInputs} from '../sdk/client'
import {inputRows} from '../sdk/task-inputs'
const props=defineProps<{task:Task;devices:Peer[];profiles:Profile[]}>()
const inputs=ref<TaskInputs>(),error=ref(''),loading=ref(false)
let revision=0
async function load(){
 const current=++revision;inputs.value=undefined;error.value='';loading.value=true
 try{const result=await client.request<TaskInputs>('/tasks/'+encodeURIComponent(props.task.id)+'/inputs');if(current===revision)inputs.value=result}
 catch(e){if(current===revision)error.value=(e as Error).message}
 finally{if(current===revision)loading.value=false}
}
onMounted(load);watch(()=>props.task.id,load)
const spec=computed(()=>inputs.value?.spec||props.task.spec)
const rows=computed(()=>inputRows(spec.value))
const attachments=computed(()=>(spec.value.asset_ids||[]).map((id:string)=>({id,asset:inputs.value?.assets.find(a=>a.id===id)})))
const images=computed(()=>attachments.value.filter((a:{id:string;asset?:Asset})=>a.asset&&isImage(a.asset)))
const urls=computed(()=>images.value.map((a:{id:string})=>client.assetURL(a.id)))
const isImage=(a:Asset)=>a.mime==='image/png'||a.mime==='image/jpeg'
const deviceName=computed(()=>props.devices.find(d=>d.id===spec.value.device_id)?.name||'手机')
const modelName=computed(()=>spec.value.model_snapshot?.name||props.profiles.find(p=>p.id===spec.value.model_profile_id)?.name||'模型配置')
</script>
<template>
 <div class="task-inputs" v-loading="loading">
  <p class="subtle">创建时保存的任务输入。这里可查看和下载附件。</p>
  <el-alert v-if="error" :title="'附件信息读取失败：'+error" type="error" :closable="false"/>
  <dl><div><dt>手机</dt><dd>{{deviceName}}<small>{{spec.device_id}}</small></dd></div><div><dt>模型配置</dt><dd>{{modelName}}<small>{{spec.model_profile_id}}</small></dd></div><div v-for="[label,value] in rows" :key="label"><dt>{{label}}</dt><dd>{{value}}</dd></div></dl>
  <h3>附件 · 按任务选择顺序</h3><p v-if="!attachments.length" class="subtle">没有附件</p>
  <div class="input-attachments"><article v-for="(item,index) in attachments" :key="item.id">
   <el-image v-if="item.asset&&isImage(item.asset)" :src="client.assetURL(item.id)" fit="contain" :preview-src-list="urls" :initial-index="images.findIndex((a:{id:string})=>a.id===item.id)" :preview-teleported="true"/>
   <strong>{{index+1}}. {{item.asset?.name||item.id}}</strong><small>{{item.asset?`${Math.ceil(item.asset.bytes/1024)} KB · ${item.asset.mime}`:'附件信息不可用'}}</small>
   <a v-if="item.asset" :href="client.assetURL(item.id)" :download="item.asset.name||item.id">下载附件</a>
  </article></div>
  <details><summary>查看完整输入 JSON</summary><pre>{{JSON.stringify(spec,null,2)}}</pre></details>
 </div>
</template>
<style scoped>
.task-inputs dl{margin:20px 0}.task-inputs dl>div{display:grid;grid-template-columns:140px minmax(0,1fr);gap:16px;padding:12px 0;border-bottom:1px solid #edf0f6}.task-inputs dt{font-size:13px;color:#748197}.task-inputs dd{margin:0;font-size:13px;white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.7}.task-inputs dd small{display:block}.input-attachments{display:grid;grid-template-columns:repeat(auto-fill,minmax(180px,1fr));gap:14px;margin:16px 0}.input-attachments article{border:1px solid #e3e8f0;border-radius:10px;padding:12px;min-width:0}.input-attachments .el-image{width:100%;height:150px}.input-attachments strong,.input-attachments small{display:block;overflow-wrap:anywhere;margin:8px 0}.input-attachments strong,.input-attachments a{font-size:12px}.task-inputs summary{cursor:pointer;color:#4167dc}.task-inputs pre{white-space:pre-wrap;overflow-wrap:anywhere;font-size:12px;line-height:1.7}@media(max-width:600px){.task-inputs dl>div{grid-template-columns:1fr;gap:6px}}
</style>

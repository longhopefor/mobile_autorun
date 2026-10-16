<script setup lang="ts">
import {ref} from 'vue'
import {client,type Peer,type Profile} from '../sdk/client'
defineProps<{profiles:Profile[];devices:Peer[]}>()
const deviceId=ref(''),profileId=ref(''),busy=ref(false),error=ref(''),results=ref<Record<string,any>[]>([])
async function probe(){
 busy.value=true;error.value=''
 try{
  const result=await client.post<Record<string,any>>('/model-profiles/'+encodeURIComponent(profileId.value)+'/test',{device_id:deviceId.value})
  results.value.unshift({...result,at:new Date().toLocaleTimeString()})
  results.value=results.value.slice(0,10)
 }catch(e){error.value=(e as Error).message}finally{busy.value=false}
}
</script>
<template>
 <section class="panel model-probe"><h3>屏幕模型只读测试</h3><p class="subtle">读取当前手机截图并调用所选模型，比较时延、用量与识别结果。建议在同一页面比较，测试会使用模型额度。</p><div class="probe-controls"><el-select v-model="deviceId" placeholder="选择在线手机"><el-option v-for="d in devices.filter(d=>d.online)" :key="d.id" :label="d.name" :value="d.id"/></el-select><el-select v-model="profileId" placeholder="选择模型配置"><el-option v-for="p in profiles" :key="p.id" :label="p.name" :value="p.id"/></el-select><el-button :disabled="!deviceId||!profileId" :loading="busy" @click="probe">只分析，不执行动作</el-button></div><p v-if="error" class="error">{{error}}</p><div v-for="(r,i) in results" :key="i" class="probe-result"><strong>{{r.provider}} · {{r.model||'CLI 默认'}} · {{(r.duration_ms/1000).toFixed(2)}} 秒</strong><small>{{r.at}} · 未执行动作<template v-if="r.result.input"> · {{r.result.input.width}}×{{r.result.input.height}} / 原图 {{r.result.input.original_width}}×{{r.result.input.original_height}}</template></small><p>{{r.result.decision.reason}}</p><details><summary>决策与用量</summary><pre>{{JSON.stringify(r.result,null,2)}}</pre></details></div></section>
</template>
<style scoped>
.model-probe{padding:22px;margin-top:22px}.probe-controls{display:flex;gap:10px;flex-wrap:wrap}.probe-controls .el-select{width:220px}.probe-result{border-top:1px solid #e7ebf3;margin-top:18px;padding-top:16px}.probe-result small{display:block;margin-top:7px;color:#748197}.probe-result pre{font-size:12px;white-space:pre-wrap;overflow-wrap:anywhere}
</style>

<script setup lang="ts">
import {computed} from 'vue'
import {client,type Asset} from '../sdk/client'
const props=defineProps<{assets:Asset[];modelValue:string[];imageOnly:boolean;busy:boolean}>()
const emit=defineEmits<{ 'update:modelValue':[ids:string[]]; upload:[imagesOnly:boolean] }>()
const isImage=(a:Asset)=>a.mime==='image/png'||a.mime==='image/jpeg'
const name=(a:Asset)=>a.name||(isImage(a)?'图片 ':'文件 ')+a.id.slice(0,8)
const choices=computed(()=>props.assets.filter(a=>!props.imageOnly||isImage(a)))
const selected=computed(()=>props.modelValue.map(id=>props.assets.find(a=>a.id===id)).filter((a):a is Asset=>!!a))
const images=computed(()=>selected.value.filter(isImage))
const previewURLs=computed(()=>images.value.map(a=>client.assetURL(a.id)))
function remove(id:string){emit('update:modelValue',props.modelValue.filter(v=>v!==id))}
</script>
<template>
 <div class="task-assets">
  <el-select :model-value="modelValue" multiple filterable :multiple-limit="9" :disabled="busy" :placeholder="imageOnly?'选择已有图片':'选择已有图片或文件'" @update:model-value="emit('update:modelValue',$event)">
   <el-option v-for="a in choices" :key="a.id" :label="name(a)" :value="a.id"/>
  </el-select>
  <div class="attachment-actions">
   <el-button size="small" :disabled="busy||modelValue.length>=9" @click="emit('upload',true)">上传图片并添加</el-button>
   <el-button v-if="!imageOnly" size="small" :disabled="busy||modelValue.length>=9" @click="emit('upload',false)">上传文件并添加</el-button>
  </div>
  <p class="attachment-hint">{{imageOnly?'按选择顺序使用，点击图片可放大预览。':'附件先传到手机；图片同时提供模型参考，其他文件不自动解析内容。'}}最多 9 个，每个不超过 16 MB。</p>
  <div v-if="selected.length" class="attachment-grid">
   <article v-for="(a,index) in selected" :key="a.id" class="task-attachment">
    <el-image v-if="isImage(a)" :src="client.assetURL(a.id)" fit="contain" :preview-src-list="previewURLs" :initial-index="images.findIndex(v=>v.id===a.id)" :preview-teleported="true" :hide-on-click-modal="true"><template #error><span>图片加载失败</span></template></el-image>
    <a v-else class="attachment-file" :href="client.assetURL(a.id)" :download="a.name">文件<br/><span>下载查看</span></a>
    <div class="attachment-info"><strong :title="name(a)">{{index+1}}. {{name(a)}}</strong><small>{{Math.ceil(a.bytes/1024)}} KB</small><el-button link type="danger" :disabled="busy" @click="remove(a.id)">移除</el-button></div>
   </article>
  </div>
 </div>
</template>

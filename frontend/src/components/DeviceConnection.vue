<script setup lang="ts">
import {computed,ref} from 'vue'
const props=defineProps<{token?:string;transport?:string}>()
function onlyTransport(u:URL){let valid=true;u.searchParams.forEach((_,k)=>{if(k!=='transport')valid=false});return valid}
const mode=ref(props.transport==='ws'?'ws':'usb')
const server=ref('ws://127.0.0.1:8080/device/connect')
const wirelessServer=ref(location.hostname==='localhost'||location.hostname==='127.0.0.1'?'':`${location.protocol==='https:'?'wss':'ws'}://${location.host}/device/connect`)
const adb=ref(''),serial=ref(''),token=ref(props.token||'')
const wsURL=computed(()=>{
 try{const u=new URL(wirelessServer.value);if(!['ws:','wss:'].includes(u.protocol)||['localhost','127.0.0.1','[::1]'].includes(u.hostname)||u.pathname!=='/device/connect'||u.username||u.password||u.hash||!onlyTransport(u))return '';u.searchParams.set('transport','ws');return u.toString()}catch{return ''}
})
const usbValid=computed(()=>{try{const u=new URL(server.value);return u.protocol==='ws:'&&['localhost','127.0.0.1','[::1]'].includes(u.hostname)&&u.pathname==='/device/connect'&&!u.username&&!u.password&&!u.hash&&onlyTransport(u)}catch{return false}})
const config=computed(()=>({mode:mode.value,server_url:mode.value==='usb'?server.value:wsURL.value,adb_binary:adb.value,serial:serial.value,...(token.value?{token:token.value}:{})}))
const valid=computed(()=>!!adb.value&&/^[A-Za-z0-9][A-Za-z0-9._:-]*$/.test(serial.value)&&(mode.value==='usb'?usbValid.value:!!wsURL.value))
function download(){const url=URL.createObjectURL(new Blob([JSON.stringify(config.value,null,2)+'\n'],{type:'application/json'}));const a=document.createElement('a');a.href=url;a.download='device.local.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}
</script>

<template>
 <div class="connection-setup">
  <el-radio-group v-model="mode"><el-radio-button value="usb">USB 连接</el-radio-button><el-radio-button value="ws">WS / WSS 连接</el-radio-button></el-radio-group>
  <p class="connection-hint">同一部手机使用原配对身份切换连接方式。切换前先接管正在执行的任务；USB 工具常驻时会维护隧道，WS 直连无需 USB 常驻。</p>
  <el-form label-position="top">
   <el-form-item v-if="mode==='usb'" label="Mac / Windows 本机工作台地址"><el-input v-model="server" placeholder="ws://127.0.0.1:8080/device/connect"/></el-form-item>
   <el-form-item v-else label="手机可访问的工作台地址"><el-input v-model="wirelessServer" placeholder="ws://电脑局域网IP:8080/device/connect 或 wss://域名/device/connect"/></el-form-item>
   <p v-if="mode==='ws'" class="connection-hint">手机和电脑处于同一局域网时填写电脑 IP，服务端需监听 0.0.0.0。公网使用已配置 TLS 的 WSS 地址。</p>
   <el-form-item label="Portal 配对 token（已有手机可留空，保留原值）"><el-input v-model="token" type="password" show-password autocomplete="off"/></el-form-item>
   <template v-if="mode==='ws'">
    <p class="connection-hint">可直接在手机 Portal → Reverse Connection 填写下面地址和原 token，然后打开 Connect to Host。</p>
    <pre v-if="wsURL" class="pair-result">{{wsURL}}</pre>
   </template>
   <h3>{{mode==='usb'?'本机 USB 工具':'可选：通过 USB 自动配置一次'}}</h3>
   <el-form-item label="ADB 可执行文件的绝对路径"><el-input v-model="adb" placeholder="例如 /absolute/path/to/adb"/></el-form-item>
   <el-form-item label="手机序列号（adb devices -l 中的第一列）"><el-input v-model="serial" placeholder="填写 adb devices -l 中的序列号"/></el-form-item>
  </el-form>
  <el-button :disabled="!valid" type="primary" @click="download">下载连接配置</el-button>
  <p class="connection-hint">将文件保存为项目的 configs/device.local.json，在项目目录执行：</p>
  <pre class="pair-result">./bin/device-connect --config configs/device.local.json{{mode==='ws'?' --once':''}}</pre>
  <p class="connection-hint">浏览器下载配置后需在本机启动工具。USB 模式等待调试授权并自动恢复断开的隧道；WS 模式配置一次后可以拔掉 USB。配置包含凭据时请妥善保存。</p>
 </div>
</template>

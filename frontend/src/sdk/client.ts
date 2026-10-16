export interface MediaAvailability {transport:"portal_native"|"usb_legacy"|"unavailable";legacy_online:boolean;reason?:string;native?:{version:number;supports_index:boolean;permission_granted:boolean}}
export interface Peer {id:string;name:string;online:boolean;media_online?:boolean;connection_transport?:"usb_reverse"|"ws";metadata:Record<string,unknown>}
export interface Profile {id:string;name:string;provider:string;model:string;connector_id?:string;base_url?:string;secret_env?:string;image_max_width?:number}
export interface Asset {id:string;name?:string;mime:string;bytes:number}
export interface Task {id:string;status:string;error:string;created_at:string;iterations:number;spec:Record<string,any>;archived_at?:string}
export interface TaskInputs {task_id:string;spec:Record<string,any>;assets:Asset[]}
export interface Step {id:number;type:string;data:Record<string,any>;created_at:string}
export interface Observation {id:string;device_id:string;asset_id:string;width:number;height:number;captured_at:string}
export class WorkbenchClient {
 public base:string
 constructor(base=import.meta.env?.VITE_API_BASE_URL || ''){this.base=base}
 async request<T>(path:string,init:RequestInit={}):Promise<T>{
  const res=await fetch(this.base+'/api/v1'+path,{...init,credentials:'include',headers:{...(init.body instanceof FormData?{}:{'Content-Type':'application/json'}),...init.headers}})
  if(!res.ok){let msg='请求失败 '+res.status;try{msg=(await res.json()).error?.code||msg}catch{}throw new Error(msg)}
  if(res.status===204||res.status===202)return undefined as T
  const body=await res.text();return (body?JSON.parse(body):undefined) as T
 }
 post<T>(path:string,body:unknown,headers:Record<string,string>={}){return this.request<T>(path,{method:'POST',body:JSON.stringify(body),headers})}
 assetURL(id:string){return this.base+'/api/v1/assets/'+encodeURIComponent(id)+'/content'}
 eventsURL(id:string,after=0){return this.base+'/api/v1/tasks/'+encodeURIComponent(id)+'/events?after='+after}
 async taskList(archived=false):Promise<Task[]>{
  const all:Task[]=[];let before=''
  for(;;){
   const page=await this.request<Task[]>('/tasks?archived='+archived+(before?'&before='+encodeURIComponent(before):''))
   all.push(...page)
   if(page.length<100)return all
   const last=page.at(-1)!.id
   if(last===before||all.length>=20000)throw new Error('任务分页超限或没有进展')
   before=last
  }
 }
 async taskSteps(id:string):Promise<Step[]>{
  const all:Step[]=[];let after=0
  for(;;){
   const page=await this.request<Step[]>('/tasks/'+encodeURIComponent(id)+'/steps?after='+after)
   if(!page.length)return all
   const last=page[page.length-1].id
   if(last<=after)throw new Error('事件分页没有进展，请刷新后重试')
   all.push(...page);after=last
   if(page.length<100)return all
   if(all.length>=20000)throw new Error('任务事件超过回放上限')
  }
 }
}
export const client=new WorkbenchClient()

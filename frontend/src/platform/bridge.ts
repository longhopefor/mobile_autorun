export interface PlatformBridge {
 pickImages():Promise<File[]>
 pickFiles():Promise<File[]>
 subscribe(url:string,onEvent:(data:string)=>void,onDisconnect:()=>void):()=>void
}
export const browserBridge:PlatformBridge={
 pickImages:()=>pickFiles('image/png,image/jpeg'),
 pickFiles:()=>pickFiles(''),
 subscribe:(url,onEvent,onDisconnect)=>{const source=new EventSource(url,{withCredentials:true});source.addEventListener('step',(e)=>onEvent((e as MessageEvent).data));source.onerror=onDisconnect;return()=>source.close()}
}

function pickFiles(accept:string):Promise<File[]>{
 return new Promise(resolve=>{const el=document.createElement('input');el.type='file';el.accept=accept;el.multiple=true;el.onchange=()=>resolve(Array.from(el.files||[]));el.oncancel=()=>resolve([]);el.click()})
}

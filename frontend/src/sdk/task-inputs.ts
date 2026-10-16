export const templateNames:Record<string,string>={general:'自然语言手机任务',xhs_image:'小红书图文',wechat_crm:'微信 AI 客服',xhs_growth:'小红书获客与运维'}
export const modeNames:Record<string,string>={edit:'编辑后停留',draft:'保存草稿',publish:'发布图文',followup:'指定客户跟进',inbox:'处理消息',research:'研究与采集',reply:'受限回复',operate:'账号运维'}
// Keep long input text intact; use the immutable model snapshot when available.
export function inputRows(spec:Record<string,any>):[string,string][]{
 const text=(v:unknown)=>v===null||v===undefined||v===''?'未填写':String(v)
 const rows:[string,string][]=[['任务模板',templateNames[spec.template]||text(spec.template)],['执行模式',modeNames[spec.mode]||text(spec.mode)],['任务目标',text(spec.goal)],['指定账号',text(spec.account)],['标题',text(spec.title)],['正文',text(spec.body)],['目标应用（不限制操作范围）',(spec.packages||[]).join('\n')||'未填写'],['计划执行时间',spec.scheduled_at?new Date(spec.scheduled_at).toLocaleString():'立即执行'],['最大迭代次数',text(spec.max_iterations)]]
 const b=spec.business
 if(b)rows.push(['主题',text(b.topic)],['回复要求',text(b.prompt)],['产品知识',text(b.knowledge)],['指定对象',(b.targets||[]).join('\n')||'未填写'],['最多采集记录',text(b.max_records)],['最多发送回复',text(b.max_replies)],['发送间隔（秒）',text(b.interval_seconds)],['添加好友',b.add_friends?'是':'否'])
 const p=spec.model_snapshot
 if(p)rows.push(['创建时模型配置',text(p.name)],['调用方式',text(p.provider)],['模型标识',p.model||'CLI 默认'],['模型截图宽度',p.image_max_width===-1?'原图':String(p.image_max_width||960)+' px'],['模型连接器',text(p.connector_id)],['API 地址',text(p.base_url)],['密钥环境变量名',text(p.secret_env)])
 return rows
}

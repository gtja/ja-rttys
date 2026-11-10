<template>
 <div class="console-app">
  <a class="skip" href="#main">跳到内容</a>
  <aside class="rail">
   <a class="logo" href="/" @click.prevent="go('devices')"><span class="logo-sign"><ConsoleIcon name="terminal"/></span><strong>rtty<span>console</span></strong></a>
   <div class="space-badge"><span class="space-avatar">{{tenantName.slice(0,1)}}</span><div><b>{{tenantName}}</b><small>当前工作空间</small></div></div>
   <div class="rail-caption">工作台</div>
   <nav aria-label="主导航"><button v-for="n in navItems" :key="n.key" :class="{selected:tab===n.key}" :aria-current="tab===n.key?'page':undefined" @click="go(n.key)"><ConsoleIcon :name="n.icon"/><span>{{n.name}}</span><small v-if="n.key==='devices'">{{allDevices.length}}</small></button></nav>
   <div class="rail-bottom"><div class="rail-help"><ConsoleIcon name="key"/><span>基于租户的访问隔离</span></div><button class="profile-button" @click="passwordDialog=true"><span class="user-avatar">{{me.user?.username?.slice(0,1).toUpperCase()||'U'}}</span><span><b>{{me.user?.username||'正在加载'}}</b><small>{{me.user?.platform_admin?'平台管理员':roleLabel(me.user?.role)}}</small></span><ConsoleIcon name="settings"/></button><button class="signout" @click="logout"><ConsoleIcon name="logout"/>退出登录</button></div>
  </aside>
  <div class="workspace-body">
   <header class="workspace-bar"><div class="crumb">工作台 <ConsoleIcon name="arrow"/><b>{{pageInfo.name}}</b></div><div class="workspace-switch"><span>切换空间</span><el-select v-model="tenant" @change="switchTenant" aria-label="当前租户" placeholder="选择租户"><el-option v-for="t in me.tenants" :key="t.id" :label="t.name" :value="t.id"/></el-select></div></header>
   <main id="main" ref="mainArea" class="workspace-main">
    <div class="heading"><div><h1>{{pageInfo.name}}</h1><p>{{pageInfo.description}}</p></div><div class="heading-actions"><el-button v-if="tab!=='ota'" :loading="loading" aria-label="刷新数据" @click="refresh"><ConsoleIcon name="refresh"/></el-button><el-button v-if="manager && !allTenants && tab==='devices'" type="primary" @click="form='device'"><ConsoleIcon name="plus"/>登记设备</el-button><el-button v-if="manager && tab==='groups'" type="primary" @click="form='group'"><ConsoleIcon name="plus"/>新建分组</el-button><el-button v-if="manager && tab==='members'" type="primary" @click="form='member'"><ConsoleIcon name="plus"/>添加成员</el-button><el-button v-if="tab==='settings' && me.user?.platform_admin" type="primary" @click="form='tenant'"><ConsoleIcon name="plus"/>新建租户</el-button></div></div>
    <el-alert v-if="error" :title="error" type="error" show-icon :closable="false" class="error-banner"/>
    <el-skeleton v-if="loading&&!loaded" :rows="8" animated class="surface"/>
    <template v-else>
     <template v-if="tab==='devices'">
      <div class="fleet-summary"><div><span class="summary-icon"><ConsoleIcon name="device"/></span><span>设备总数<strong>{{allDevices.length}}</strong></span></div><div><i class="dot green"/>在线<strong>{{onlineCount}}</strong></div><div><i class="dot"/>离线<strong>{{allDevices.filter(d=>!d.online&&!d.disabled).length}}</strong></div><div class="summary-context">{{allTenants ? '平台全部租户的设备' : '当前租户内的可见设备'}}</div></div>
      <section class="surface">
       <div class="table-controls"><div class="filter-tabs" aria-label="设备状态"><button v-for="f in deviceFilters" :key="f.value" :class="{active:deviceFilter===f.value}" @click="deviceFilter=f.value;devicePage=1">{{f.label}}</button></div><el-input v-model="search" @input="devicePage=1" placeholder="搜索名称、-I 编号、-D 描述或分组" aria-label="搜索设备" clearable class="search-input"><template #prefix><ConsoleIcon name="search"/></template></el-input></div>
       <el-table :data="pagedDevices" row-key="id" empty-text="没有符合条件的设备" class="device-table">
        <el-table-column label="设备" min-width="230"><template #default="{row}"><div class="identity-cell"><span class="machine-icon"><ConsoleIcon name="device"/></span><div><b>{{row.description||row.name||row.wire_id}}</b><small>{{row.wire_id}}</small></div></div></template></el-table-column>
        <el-table-column v-if="allTenants" prop="tenant_name" label="所属租户" min-width="150"/>
        <el-table-column label="注册参数（-I / -D）" min-width="240"><template #default="{row}"><div class="registration-params"><span><b>-I</b> <code>{{row.client_id || (row.online ? '等待上报' : '离线，连接后显示')}}</code></span><span><b>-D</b> {{row.last_registered_at ? (row.client_description || '（空描述）') : '尚未注册上报'}}</span></div></template></el-table-column>
        <el-table-column label="状态" width="110"><template #default="{row}"><span :class="['state-label',{online:row.online,disabled:row.disabled}]"><i/>{{row.disabled?'已停用':row.online?'在线':'离线'}}</span></template></el-table-column>
        <el-table-column label="PSDK 运行镜像" min-width="230"><template #default="{row}"><div class="registration-params"><span>{{row.runtime?.info?.image||'尚未采集'}}</span><span>{{row.runtime?.collected_at?new Date(row.runtime.collected_at*1000).toLocaleString():''}} {{row.runtime?.status==='failed'?' · 采集失败':row.runtime?.status==='collecting'?' · 采集中':''}}</span></div></template></el-table-column>
        <el-table-column label="所属分组" min-width="230"><template #default="{row}"><div class="tags"><span v-for="g in groupsFor(row)" :key="g">{{g}}</span><em v-if="!groupsFor(row).length">未分组</em></div></template></el-table-column>
        <el-table-column label="连接链路" width="180"><template #default="{row}"><el-select v-model="linkSelections[row.id]" placeholder="自动优选" clearable><el-option v-for="l in row.links||[]" :key="l.link" :value="l.link" :label="`${l.link==='14g'?'1.4G':l.link==='5g'?'5G':l.link==='default'?'默认':l.link} · 在线`"/></el-select></template></el-table-column>
        <el-table-column label="已连接时长" width="150"><template #default="{row}"><span class="secondary">{{row.online?duration(row.connected):'—'}}</span></template></el-table-column>
        <el-table-column label="运行时长" width="150"><template #default="{row}"><span class="secondary">{{!row.online?'—':row.uptime?duration(row.uptime):'等待上报'}}</span></template></el-table-column>
        <el-table-column label="操作" width="460" align="right"><template #default="{row}"><div class="row-actions"><el-button v-if="allTenants" :disabled="row.tenant_disabled" type="primary" plain @click="enterTenant(row)">进入租户</el-button><template v-else><el-button text @click="detail={...row,preferred_link:linkSelections[row.id]||''}">{{enabled('files')?'详情 / 文件':'详情'}}</el-button><el-button v-if="manager" text @click="editDevice(row)">编辑</el-button><el-button :disabled="!row.online||!row.can_operate" type="primary" plain @click="connect(row)"><ConsoleIcon name="terminal"/>终端</el-button><el-button v-if="manager&&enabled('ota')" text :disabled="row.disabled" @click="upgradeDevice(row)">程序升级</el-button><el-button :disabled="!row.online||!row.can_operate||!enabled('web')" text @click="web.dev={...row,preferred_link:linkSelections[row.id]||''};web.modal=true">Web</el-button><el-dropdown v-if="manager" trigger="click" @command="cmd=>deviceAction(cmd,row)"><el-button text aria-label="设备更多操作"><ConsoleIcon name="more"/></el-button><template #dropdown><el-dropdown-menu><el-dropdown-item command="groups">设置分组</el-dropdown-item><el-dropdown-item command="rotate">重置 Token</el-dropdown-item><el-dropdown-item command="disable" divided>{{row.disabled?'启用设备':'停用设备'}}</el-dropdown-item><el-dropdown-item command="delete" divided>软删除设备</el-dropdown-item></el-dropdown-menu></template></el-dropdown></template></div></template></el-table-column>
       </el-table>
       <div class="table-bottom"><span>共 {{filteredDevices.length}} 台设备</span><el-pagination v-model:current-page="devicePage" :page-size="12" :total="filteredDevices.length" layout="prev, pager, next" hide-on-single-page/></div>
      </section>
      <p class="footnote">终端内支持文件上传与下载。设备操作权限由当前成员的有效授权决定。</p>
     </template>
     <section v-if="tab==='groups'&&manager" class="split-surface">
      <div class="collection-list"><div class="collection-title">全部分组 <span>{{snapshot.groups?.length||0}}</span></div><button v-for="g in snapshot.groups" :key="g.name" :class="['collection-item',{selected:selectedGroup===g.name}]" @click="selectedGroup=g.name"><span class="folder-icon"><ConsoleIcon name="folder"/></span><span><b>{{g.name}}</b><small>{{groupDevices(g.name).length}} 台设备</small></span><ConsoleIcon name="arrow"/></button><div v-if="!snapshot.groups?.length" class="empty-small">暂无分组，先创建一个项目或团队分组。</div></div>
      <div class="collection-detail" v-if="selectedGroup && snapshot.groups?.some(g=>g.name===selectedGroup)"><div class="detail-heading"><div><span class="section-kicker">设备分组</span><h2>{{selectedGroup}}</h2><p>包含 {{groupDevices(selectedGroup).length}} 台设备 · 设备可同时属于多个分组</p></div><el-dropdown trigger="click" @command="groupAction"><el-button>管理分组 <ConsoleIcon name="more"/></el-button><template #dropdown><el-dropdown-menu><el-dropdown-item command="rename">重命名</el-dropdown-item><el-dropdown-item command="delete" divided>删除分组</el-dropdown-item></el-dropdown-menu></template></el-dropdown></div><el-table :data="groupDevices(selectedGroup)" empty-text="这个分组还没有设备，请在设备设置中勾选该分组。"><el-table-column label="设备" min-width="180"><template #default="{row}"><b>{{row.description||row.name||row.wire_id}}</b><div class="secondary mono">{{row.wire_id}}</div></template></el-table-column><el-table-column label="状态" width="100"><template #default="{row}"><span :class="['state-label',{online:row.online}]"><i/>{{row.online?'在线':'离线'}}</span></template></el-table-column><el-table-column width="130" align="right"><template #default="{row}"><el-button text @click="editGroups(row)">调整分组</el-button></template></el-table-column></el-table><div class="context-note"><ConsoleIcon name="key"/><p>成员拥有任意所属组的授权，或设备的直接授权，即可访问设备。移出一个组不会影响其他有效授权。</p></div><el-button @click="go('devices')">前往设备列表添加设备 <ConsoleIcon name="arrow"/></el-button></div>
      <div v-else class="empty-detail"><ConsoleIcon name="folder"/><h2>选择一个分组</h2><p>查看组内设备并调整设备归属。</p></div>
     </section>
     <section v-if="tab==='members'&&manager" class="split-surface member-layout">
      <div class="collection-list"><div class="collection-title">租户成员 <span>{{snapshot.members?.length||0}}</span></div><button v-for="u in snapshot.members" :key="u.id" :class="['collection-item',{selected:selectedMember===u.id}]" @click="selectMember(u)"><span class="member-avatar">{{u.username.slice(0,1).toUpperCase()}}</span><span><b>{{u.username}}</b><small>{{roleLabel(u.role)}}</small></span><span class="self-tag" v-if="u.id===me.user?.id">你</span></button><div v-if="!snapshot.members?.length" class="empty-small">当前租户暂无成员。</div></div>
      <div v-if="activeMember" class="collection-detail"><div class="detail-heading"><div><span class="section-kicker">{{roleLabel(activeMember.role)}}</span><h2>{{activeMember.username}}</h2><p>{{activeMember.role==='admin'?'可管理本租户全部设备与成员':activeMember.role==='viewer'?'仅查看设备信息，不允许远程操作':'按设备或分组授权访问'}}</p></div><el-button :disabled="activeMember.id===me.user?.id" text type="danger" @click="confirmRemoveMember(activeMember)">移除成员</el-button></div>
       <div class="permissions-heading"><h3>访问范围</h3><el-button v-if="activeMember.role==='operator'" type="primary" plain @click="grant.user_id=activeMember.id;grant.device_id='';grant.group='';form='grant'"><ConsoleIcon name="plus"/>添加授权</el-button></div>
       <div v-if="activeMember.role!=='operator'" class="role-explanation"><ConsoleIcon name="key"/><div><b>{{activeMember.role==='admin'?'全部设备':'只读访问'}}</b><p>{{activeMember.role==='admin'?'租户管理员无需单独配置设备授权。':'查看者不会因设备或分组授权获得远程操作权限。'}}</p></div></div>
       <el-table v-else :data="memberGrants" empty-text="尚未授权任何设备或分组"><el-table-column label="授权对象" min-width="180"><template #default="{row}"><span class="permission-kind">{{row.device_id?'设备':'分组'}}</span>{{row.target}}</template></el-table-column><el-table-column width="90" align="right"><template #default="{row}"><el-button text type="danger" @click="revokeGrant(row)">撤销</el-button></template></el-table-column></el-table>
       <div class="context-note"><ConsoleIcon name="key"/><p>权限取并集。只有最后一条有效授权被撤销时，正在使用的终端、文件和 Web 会话才会断开。</p></div>
       <el-button v-if="me.user?.platform_admin" @click="form='existing-member'">将已有账号加入租户</el-button>
      </div><div v-else class="empty-detail"><ConsoleIcon name="users"/><h2>选择一位成员</h2><p>查看成员角色与设备访问范围。</p></div>
     </section>
     <BatchTasks v-if="tab==='tasks'&&manager&&!allTenants" :key="tenant" :devices="allDevices"/>
     <DevicePresence v-if="tab==='presence'" :key="me.user?.tenant_id||'all'"/>
     <OtaPanel v-if="tab==='ota'&&manager" :key="`${tenant}-${otaDevice?.id||'all'}`" :initial-device="otaDevice"/>
     <section v-if="tab==='audit'&&manager" class="surface"><div class="table-controls"><div><strong>最近操作</strong><span class="secondary"> · 最近 100 条记录</span></div><el-input v-model="auditSearch" @input="auditPage=1" clearable placeholder="搜索操作或对象" aria-label="搜索操作记录" class="search-input"><template #prefix><ConsoleIcon name="search"/></template></el-input></div><el-table :data="pagedAudit" empty-text="暂无匹配记录"><el-table-column label="时间" width="185"><template #default="{row}"><span class="mono secondary">{{formatTime(row.at)}}</span></template></el-table-column><el-table-column label="操作" min-width="180"><template #default="{row}">{{actionLabel(row.action)}}</template></el-table-column><el-table-column label="对象" min-width="180" show-overflow-tooltip><template #default="{row}">{{resourceLabel(row.resource)}}</template></el-table-column><el-table-column label="执行人" width="150"><template #default="{row}">{{resourceLabel(row.user)}}</template></el-table-column></el-table><div class="table-bottom"><span>共 {{filteredAudit.length}} 条记录</span><el-pagination v-model:current-page="auditPage" :page-size="15" :total="filteredAudit.length" layout="prev, pager, next" hide-on-single-page/></div></section>
     <template v-if="tab==='settings'&&me.user?.platform_admin"><section class="surface"><div class="table-controls"><strong>租户空间</strong></div><el-table :data="snapshot.tenants"><el-table-column prop="name" label="租户"/><el-table-column label="状态" width="110"><template #default="{row}">{{row.disabled?'已停用':'启用中'}}</template></el-table-column><el-table-column label="功能授权" min-width="300"><template #default="{row}"><div class="tenant-feature-tags"><span v-for="f in featureOptions" :key="f.key" :class="{off:row.features?.[f.key]===false}">{{f.label}}</span></div><p v-if="row.features?.ota&&!row.ota_backend_ready" class="secondary">OTA 已授权，后端尚未配置</p></template></el-table-column><el-table-column width="180" align="right"><template #default="{row}"><el-button text @click="editFeatures(row)">功能授权</el-button><el-button text @click="confirmDisable('tenants',row)">{{row.disabled?'启用':'停用'}}</el-button></template></el-table-column></el-table></section><section class="surface spaced"><div class="table-controls"><strong>平台账号</strong></div><el-table :data="snapshot.users"><el-table-column prop="username" label="账号"/><el-table-column label="状态"><template #default="{row}">{{row.disabled?'已停用':'启用中'}}</template></el-table-column><el-table-column align="right"><template #default="{row}"><el-button text :disabled="row.id===me.user?.id" @click="confirmDisable('users',row)">{{row.disabled?'启用':'停用'}}</el-button></template></el-table-column></el-table></section></template>
    </template>
   </main>
  </div>
  <el-drawer :model-value="!!form" @close="form=''" :title="formTitles[form]" size="min(480px, 100vw)" class="console-drawer">
   <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon/>
   <el-form label-position="top" class="drawer-form" @submit.prevent="submitForm">
    <template v-if="form==='device'"><p class="drawer-intro">登记后签发独立凭据，用于设备首次连接。请妥善保存。</p><el-form-item label="设备编号"><el-input v-model="device.wire_id" placeholder="平台登记编号，可与客户端 -I 不同" aria-label="设备编号"/></el-form-item><el-form-item label="显示名称"><el-input v-model="device.name" placeholder="例如：研发中心边缘盒子"/></el-form-item><el-form-item label="所属分组"><el-checkbox-group v-model="enrollGroups" class="checkbox-list"><el-checkbox v-for="g in snapshot.groups" :key="g.name" :label="g.name" :value="g.name"/></el-checkbox-group><span v-if="!snapshot.groups?.length" class="secondary">尚无分组，可登记后设置。</span></el-form-item><el-form-item label="允许的 Web 目标"><el-input v-model="webTargets" type="textarea" :rows="3" placeholder="http://127.0.0.1:8080"/></el-form-item><p class="field-help">多个目标用逗号分隔，仅允许明确配置的地址。</p></template>
    <template v-if="form==='group'"><p class="drawer-intro">按项目、站点或维护团队组织设备。</p><el-form-item label="分组名称"><el-input v-model="newGroup" placeholder="例如：杭州项目" aria-label="新分组名称"/></el-form-item></template>
    <template v-if="form==='member'"><p class="drawer-intro">创建账号并加入当前租户。运维成员需另行配置设备授权。</p><el-form-item label="用户名"><el-input v-model="account.username" autocomplete="off"/></el-form-item><el-form-item label="初始密码"><el-input v-model="account.password" type="password" show-password autocomplete="new-password" placeholder="至少 12 个英文字符、数字或符号"/><p class="field-help">密码需为 12–72 字节；英文、数字和常用符号每个占 1 字节。</p></el-form-item><el-form-item label="成员角色"><el-radio-group v-model="account.role" class="role-options"><el-radio value="operator">运维成员<span>按授权访问设备</span></el-radio><el-radio value="viewer">查看者<span>仅查看设备信息</span></el-radio><el-radio value="admin">租户管理员<span>管理本租户设备与成员</span></el-radio></el-radio-group></el-form-item></template>
    <template v-if="form==='grant'"><p class="drawer-intro">为 {{activeMember?.username}} 添加一个访问范围。</p><el-form-item label="授权类型"><el-radio-group v-model="grantKind" @change="grant.device_id='';grant.group=''"><el-radio-button value="group">设备分组</el-radio-button><el-radio-button value="device">单台设备</el-radio-button></el-radio-group></el-form-item><el-form-item v-if="grantKind==='group'" label="分组"><el-select v-model="grant.group" placeholder="选择分组"><el-option v-for="g in snapshot.groups" :key="g.name" :label="g.name" :value="g.name"/></el-select></el-form-item><el-form-item v-else label="设备"><el-select v-model="grant.device_id" placeholder="选择设备"><el-option v-for="d in snapshot.devices" :key="d.id" :label="d.name||d.wire_id" :value="d.id"/></el-select></el-form-item></template>
    <template v-if="form==='tenant'"><el-form-item label="租户名称"><el-input v-model="newTenant" placeholder="请输入租户名称"/></el-form-item></template>
    <template v-if="form==='existing-member'"><el-form-item label="已有账号"><el-select v-model="member.user_id" placeholder="选择账号"><el-option v-for="u in snapshot.users" :key="u.id" :label="u.username" :value="u.id"/></el-select></el-form-item><el-form-item label="角色"><el-select v-model="member.role"><el-option label="运维成员" value="operator"/><el-option label="查看者" value="viewer"/><el-option label="租户管理员" value="admin"/></el-select></el-form-item></template>
   </el-form>
   <template #footer><div class="drawer-footer"><el-button @click="form=''">取消</el-button><el-button type="primary" :loading="busy" @click="submitForm">{{form==='device'?'登记并签发凭据':form==='grant'?'确认授权':'保存'}}</el-button></div></template>
  </el-drawer>
  <el-dialog :model-value="!!credential" @close="credential=''" title="设备接入凭据" width="min(520px, 94vw)" class="console-dialog"><el-alert title="凭据仅显示一次，请保存后再关闭。" type="warning" :closable="false"/><pre class="credential">{{credential}}</pre><template #footer><el-button type="primary" @click="credential=''">已保存，关闭</el-button></template></el-dialog>
  <el-dialog v-model="groupEdit.open" title="设置设备分组" width="min(460px, 94vw)" class="console-dialog"><p class="drawer-intro">{{groupEdit.label}} · 可同时选择多个分组</p><el-alert v-if="error" :title="error" type="error" :closable="false"/><el-checkbox-group v-model="groupEdit.groups" class="checkbox-list" aria-label="设备分组"><el-checkbox v-for="g in snapshot.groups" :key="g.name" :label="g.name" :value="g.name"/></el-checkbox-group><p class="field-help">只有失去全部有效授权的成员才会断开会话。</p><template #footer><el-button @click="groupEdit.open=false">取消</el-button><el-button type="primary" :loading="busy" @click="saveGroups">保存分组</el-button></template></el-dialog>
  <el-dialog v-model="renameGroup.open" title="重命名分组" width="min(420px, 94vw)" class="console-dialog"><el-input v-model="renameGroup.name" aria-label="分组新名称"/><p class="field-help">设备关联和已有授权保持不变。</p><el-alert v-if="error" :title="error" type="error" :closable="false"/><template #footer><el-button @click="renameGroup.open=false">取消</el-button><el-button type="primary" @click="saveGroupName">保存名称</el-button></template></el-dialog>
  <el-dialog v-model="passwordDialog" title="修改登录密码" width="min(420px, 94vw)" class="console-dialog"><el-form label-position="top" @submit.prevent="changePassword"><el-form-item label="当前密码"><el-input v-model="password.current_password" type="password" autocomplete="current-password"/></el-form-item><el-form-item label="新密码"><el-input v-model="password.new_password" type="password" autocomplete="new-password" placeholder="12–72 字节"/></el-form-item><el-button type="primary" @click="changePassword">修改并重新登录</el-button></el-form></el-dialog>
  <el-drawer v-model="deviceEdit.open" title="编辑设备" size="min(480px,100vw)" class="console-drawer" :close-on-click-modal="!busy">
   <p class="drawer-intro">修改名称和分组，设备编号与接入 Token 保持不变。</p><el-alert v-if="error" :title="error" type="error" :closable="false"/>
   <el-form label-position="top" class="drawer-form" @submit.prevent="saveDeviceEdit"><el-form-item label="平台登记编号"><el-input :model-value="deviceEdit.wire_id" disabled/></el-form-item><el-form-item label="设备名称"><el-input v-model="deviceEdit.name" maxlength="128" aria-label="编辑设备名称"/></el-form-item><el-form-item label="所属分组"><el-checkbox-group v-model="deviceEdit.groups" class="checkbox-list" aria-label="编辑设备分组"><el-checkbox v-for="g in snapshot.groups||[]" :key="g.name" :value="g.name">{{g.name}}</el-checkbox></el-checkbox-group><p v-if="!snapshot.groups?.length" class="field-help">尚无分组，请先到分组页面新建。</p></el-form-item></el-form>
   <p class="field-help">可同时属于多个分组。移出分组后，失去全部有效授权的成员会断开远程会话。</p>
   <template #footer><el-button :disabled="busy" @click="deviceEdit.open=false">取消</el-button><el-button type="primary" :loading="busy" @click="saveDeviceEdit">保存设备</el-button></template>
  </el-drawer>
  <el-drawer v-model="featureEdit.open" :title="featureEdit.name+' · 功能授权'" size="min(520px,100vw)" class="console-drawer" :close-on-click-modal="!busy">
   <p class="drawer-intro">为租户配置可使用的功能。成员仍需具备相应角色与设备授权；远程终端保留为基础能力。</p><el-alert v-if="error" :title="error" type="error" :closable="false"/>
   <div class="feature-options"><label v-for="f in featureOptions" :key="f.key"><span><b>{{f.label}}</b><small>{{f.description}}</small></span><el-switch v-model="featureEdit.features[f.key]" :aria-label="f.label+'授权'" :disabled="busy"/></label></div>
   <el-alert v-if="featureEdit.features.ota&&!featureEdit.otaReady" title="OTA 已选择授权，但此租户的升级后端尚未配置，暂时不能发布升级。" type="warning" show-icon :closable="false"/>
   <p class="field-help">关闭文件或 Web 功能会撤销对应通道，保留终端。已下发的升级与设备命令不会因关闭授权而自动撤回。</p>
   <template #footer><el-button :disabled="busy" @click="featureEdit.open=false">取消</el-button><el-button type="primary" :loading="busy" @click="saveFeatures">保存授权</el-button></template>
  </el-drawer>
  <DeviceOverview v-if="detail" :key="detail.id" :device="detail" :platform-admin="!!me.user?.platform_admin" :files-enabled="enabled('files')" @close="detail=null"/>
  <RttyWeb v-model="web.modal" :dev="web.dev"/>
 </div>
</template>

<script setup>
import {ref,reactive,computed,onMounted,watch,nextTick} from 'vue'
import axios from 'axios'
import {ElMessageBox} from 'element-plus'
import {useRouter} from 'vue-router'
import ConsoleIcon from '../components/ConsoleIcon.vue'
import RttyWeb from '../components/RttyWeb.vue'
import OtaPanel from '../components/OtaPanel.vue'
import DevicePresence from '../components/DevicePresence.vue'
import DeviceOverview from '../components/DeviceOverview.vue'
import BatchTasks from '../components/BatchTasks.vue'
const detail=ref(null),otaDevice=ref(null),linkSelections=reactive({})
const router=useRouter(),me=ref({tenants:[]}),tenant=ref(''),devices=ref([]),snapshot=ref({}),error=ref(''),tab=ref('devices'),search=ref(''),newTenant=ref(''),credential=ref(''),webTargets=ref('')
const account=reactive({username:'',password:'',role:'operator'}),member=reactive({user_id:'',role:'operator'}),device=reactive({wire_id:'',name:'',group:''}),grant=reactive({user_id:'',device_id:'',group:''}),web=reactive({modal:false,dev:null})
const deviceEdit=reactive({open:false,id:'',wire_id:'',name:'',groups:[]})
const featureEdit=reactive({open:false,id:'',name:'',otaReady:false,features:{ota:true,tasks:true,files:true,web:true}})
const featureOptions=[{key:'ota',label:'应用升级 OTA',description:'制作版本、发布升级并查看执行结果'},{key:'tasks',label:'批量任务',description:'向授权设备发起预设巡检任务'},{key:'files',label:'文件管理',description:'浏览目录与上传、下载文件'},{key:'web',label:'Web 代理',description:'访问设备允许的 Web 服务'}]
const enabled=key=>me.value.features?.[key]!==false
function editDevice(d){error.value='';Object.assign(deviceEdit,{open:true,id:d.id,wire_id:d.wire_id,name:d.name||d.description||d.wire_id,groups:[...groupsFor(d)]})}
async function saveDeviceEdit(){if(!deviceEdit.name.trim()){error.value='请填写设备名称';return}if(await action(()=>axios.put(`/api/manage/devices/${deviceEdit.id}/details`,{name:deviceEdit.name.trim(),groups:deviceEdit.groups})))deviceEdit.open=false}
function editFeatures(t){error.value='';Object.assign(featureEdit,{open:true,id:t.id,name:t.name,otaReady:!!t.ota_backend_ready,features:{ota:true,tasks:true,files:true,web:true,...t.features}})}
async function saveFeatures(){if(await action(()=>axios.put(`/api/manage/tenants/${featureEdit.id}/features`,{features:{...featureEdit.features}})))featureEdit.open=false}
const newGroup=ref(''),enrollGroups=ref([]),groupEdit=reactive({open:false,id:'',label:'',groups:[]}),renameGroup=reactive({open:false,old:'',name:''})
const loading=ref(false),loaded=ref(false)
const passwordDialog=ref(false),password=reactive({current_password:'',new_password:''})
const allTenants=computed(()=>me.value.user?.platform_admin&&!tenant.value)
async function enterTenant(d){tenant.value=d.tenant_id;await switchTenant()}
const manager=computed(()=>me.value.user?.platform_admin||me.value.user?.role==='admin')
const grantRows=computed(()=>(snapshot.value.grants||[]).map(g=>({...g,username:snapshot.value.members?.find(u=>u.id===g.user_id)?.username||g.user_id,target:g.device_id?(snapshot.value.devices?.find(d=>d.id===g.device_id)?.wire_id||g.device_id):`设备组：${g.group}`})))
async function refresh(){loading.value=true;try{me.value=(await axios.get('/api/me')).data;tenant.value=me.value.user.tenant_id;devices.value=(await axios.get('/api/devs')).data;snapshot.value=manager.value?(await axios.get('/api/manage/snapshot')).data:{}}catch(e){if(e.response?.status===401)router.push('/login');else error.value='加载失败，请重试'}finally{loading.value=false;loaded.value=true}}
async function action(fn){if(busy.value)return false;busy.value=true;error.value='';try{await fn();await refresh();return true}catch(e){error.value=e.response?.data?.error||`操作未完成（${e.response?.status||'网络错误'}）`;return false}finally{busy.value=false}}
async function switchTenant(){otaDevice.value=null;detail.value=null;form.value='';selectedGroup.value='';selectedMember.value='';devicePage.value=1;auditPage.value=1;snapshot.value={};devices.value=[];search.value='';credential.value='';enrollGroups.value=[];grant.device_id='';grant.group='';grant.user_id='';groupEdit.open=false;renameGroup.open=false;deviceEdit.open=false;featureEdit.open=false;await action(()=>axios.post('/api/tenant',{tenant_id:tenant.value}))}
async function logout(){await axios.post('/api/signout',{});router.push('/login')}
async function changePassword(){try{await axios.post('/api/password',password);password.current_password='';password.new_password='';passwordDialog.value=false;router.push('/login')}catch(e){error.value=`修改密码失败（${e.response?.status||'网络错误'}）`;passwordDialog.value=false}}
function connect(d){window.open(`/rtty/${encodeURIComponent(d.id)}?link=${encodeURIComponent(linkSelections[d.id]||'')}`,'_blank','noopener')}
async function createTenant(){await action(async()=>{const {data}=await axios.post('/api/manage/tenants',{name:newTenant.value});newTenant.value='';await axios.post('/api/tenant',{tenant_id:data.id})})}
async function createUser(){
 const bytes=new TextEncoder().encode(account.password).length
 if(bytes<12||bytes>72){error.value=`密码长度需为 12–72 字节，当前为 ${bytes} 字节；英文、数字和常用符号每个占 1 字节`;return}
 await action(async()=>{const {data}=await axios.post('/api/manage/users',{username:account.username,password:account.password});account.password='';if(tenant.value)await axios.put('/api/manage/members',{user_id:data.id,tenant_id:tenant.value,role:account.role});account.username=''})}
async function addMember(){await action(()=>axios.put('/api/manage/members',{...member,tenant_id:tenant.value}))}
async function removeMember(u){await action(()=>axios.delete(`/api/manage/members/${u.id}`))}
async function changeDisabled(kind,row){await action(()=>axios.patch(`/api/manage/${kind}/${row.id}`,{disabled:!row.disabled}))}
async function enroll(){await action(async()=>{const {data}=await axios.post('/api/manage/devices',{...device,web_targets:webTargets.value.split(',').map(s=>s.trim()).filter(Boolean)});credential.value=`设备编号：${data.wire_id}\nToken：${data.token}`;if(enrollGroups.value.length)await axios.put(`/api/manage/devices/${data.id}/groups`,{groups:enrollGroups.value})})}
async function rotate(d){await action(async()=>{const {data}=await axios.post(`/api/manage/devices/${d.id}/rotate`,{confirm_wire_id:d.wire_id});credential.value=`设备编号：${d.wire_id}\n新 Token：${data.token}`})}
async function changeGrant(access){await action(()=>axios.put('/api/manage/grants',{...grant,access}))}
function groupsFor(d){return d.groups||(snapshot.value.device_groups||[]).filter(g=>g.device_id===d.id).map(g=>g.group)}
async function createGroup(){await action(async()=>{await axios.post('/api/manage/groups',{name:newGroup.value});newGroup.value=''})}
function editGroups(d){Object.assign(groupEdit,{open:true,id:d.id,label:d.wire_id,groups:[...groupsFor(d)]})}
async function saveGroups(){if(await action(()=>axios.put(`/api/manage/devices/${groupEdit.id}/groups`,{groups:groupEdit.groups})))groupEdit.open=false}
async function saveGroupName(){if(await action(()=>axios.patch(`/api/manage/groups/${encodeURIComponent(renameGroup.old)}`,{name:renameGroup.name})))renameGroup.open=false}
async function deleteGroup(g){try{await ElMessageBox.confirm(`删除“${g.name}”会移除设备关联及成员授权，设备不会删除。没有其他授权的会话将断开。`,'删除分组',{type:'warning',confirmButtonText:'删除分组',cancelButtonText:'取消'})}catch{return}await action(()=>axios.delete(`/api/manage/groups/${encodeURIComponent(g.name)}`))}
onMounted(refresh)


const form=ref(''),busy=ref(false),mainArea=ref(null),selectedGroup=ref(''),selectedMember=ref(''),deviceFilter=ref('all'),devicePage=ref(1),auditPage=ref(1),auditSearch=ref(''),grantKind=ref('group')
const deviceFilters=[{value:'all',label:'全部设备'},{value:'online',label:'在线'},{value:'offline',label:'离线'},{value:'disabled',label:'已停用'}]
const nav=[{key:'devices',icon:'device',name:'设备',description:'连接、管理你的远程设备。'},{key:'presence',icon:'history',name:'在线记录与统计',description:'查看每次上下线、最近上线设备与在线时长。'},{key:'groups',icon:'folder',name:'分组',description:'按项目和团队组织设备，统一管理访问范围。'},{key:'members',icon:'users',name:'成员与权限',description:'让每一位成员拥有明确的访问范围。'},{key:'tasks',icon:'terminal',name:'批量任务',description:'按设备与分组执行只读巡检，查看逐台结果。'},{key:'ota',icon:'device',name:'应用升级',description:'发布业务程序版本并追踪设备执行结果。'},{key:'audit',icon:'history',name:'操作记录',description:'追踪设备访问与管理变更。'},{key:'settings',icon:'settings',name:'租户与账号',description:'管理平台工作空间和账号状态。'}]
const navItems=computed(()=>nav.filter(n=>(!['ota','tasks'].includes(n.key)||enabled(n.key))&&(!allTenants.value||['devices','presence','settings'].includes(n.key))&&(['devices','presence'].includes(n.key)||(manager.value&&(n.key!=='settings'||me.value.user?.platform_admin)))))
const pageInfo=computed(()=>nav.find(n=>n.key===tab.value)||nav[0])
const tenantName=computed(()=>me.value.tenants?.find(t=>t.id===tenant.value)?.name||'选择租户')
const allDevices=computed(()=>manager.value&&!allTenants.value?(snapshot.value.devices||[]).map(d=>({...d,...devices.value.find(x=>x.id===d.id),groups:groupsFor(d)})):devices.value)
const filteredDevices=computed(()=>allDevices.value.filter(d=>(deviceFilter.value==='all'||(deviceFilter.value==='online'?d.online:deviceFilter.value==='disabled'?d.disabled:!d.online&&!d.disabled))&&`${d.wire_id} ${d.description||d.name||''} ${d.client_description||''} ${(d.links||[]).map(l=>l.client_id||'').join(' ')} ${d.tenant_name||''} ${groupsFor(d).join(' ')}`.toLowerCase().includes(search.value.toLowerCase())))
const pagedDevices=computed(()=>filteredDevices.value.slice((devicePage.value-1)*12,devicePage.value*12))
const onlineCount=computed(()=>devices.value.filter(d=>d.online).length)
const activeMember=computed(()=>snapshot.value.members?.find(u=>u.id===selectedMember.value))
const memberGrants=computed(()=>grantRows.value.filter(g=>g.user_id===selectedMember.value))
const filteredAudit=computed(()=>(snapshot.value.audit||[]).filter(a=>`${actionLabel(a.action)} ${resourceLabel(a.resource)}`.toLowerCase().includes(auditSearch.value.toLowerCase())))
const pagedAudit=computed(()=>filteredAudit.value.slice((auditPage.value-1)*15,auditPage.value*15))
const formTitles={device:'登记设备',group:'新建分组',member:'添加成员',grant:'添加访问授权',tenant:'新建租户','existing-member':'添加已有账号'}
watch(snapshot,()=>{if(!snapshot.value.groups?.some(g=>g.name===selectedGroup.value))selectedGroup.value=snapshot.value.groups?.[0]?.name||'';if(!snapshot.value.members?.some(u=>u.id===selectedMember.value))selectedMember.value=snapshot.value.members?.[0]?.id||''})
watch(navItems,items=>{if(!items.some(n=>n.key===tab.value))tab.value='devices'})
async function upgradeDevice(d){otaDevice.value={id:d.id,wire_id:d.wire_id,name:d.name||d.description||d.wire_id};await go('ota',true)}
async function go(key,fromDevice=false){if(!fromDevice)otaDevice.value=null;tab.value=key;error.value='';await nextTick();mainArea.value?.scrollTo({top:0})}
function groupDevices(name){return allDevices.value.filter(d=>groupsFor(d).includes(name))}
function selectMember(u){selectedMember.value=u.id;grant.user_id=u.id}
function duration(seconds){const n=Math.max(0,Math.floor(Number(seconds)||0));const days=Math.floor(n/86400);const clock=[Math.floor(n%86400/3600),Math.floor(n%3600/60),n%60].map(x=>String(x).padStart(2,'0')).join(':');return `${days?days+' 天 ':''}${clock}`}
function roleLabel(role){return {admin:'租户管理员',operator:'运维成员',viewer:'查看者'}[role]||'未选择租户'}
function formatTime(at){return new Date(Number(at)*1000).toLocaleString('zh-CN',{hour12:false})}
function resourceLabel(id){return snapshot.value.devices?.find(d=>d.id===id)?.wire_id||snapshot.value.members?.find(u=>u.id===id)?.username||id||'—'}
function actionLabel(action){return {'access-open':'开启远程会话','access-close':'关闭远程会话','POST /api/manage/groups':'创建分组','PATCH /api/manage/groups/:name':'重命名分组','DELETE /api/manage/groups/:name':'删除分组','PUT /api/manage/devices/:id/groups':'更新设备分组','PUT /api/manage/grants':'更新访问授权','POST /api/manage/devices':'登记设备','DELETE /api/manage/devices/:id':'软删除设备','POST /api/manage/devices/:id/rotate':'重置设备 Token','PUT /api/manage/members':'更新成员角色','DELETE /api/manage/members/:id':'移除成员','POST /api/manage/users':'创建账号'}[action]||action}
async function submitForm(){const createdGroup=newGroup.value;const actions={device:enroll,group:createGroup,member:createUser,grant:()=>changeGrant(true),tenant:createTenant,'existing-member':addMember};if(actions[form.value]){await actions[form.value]();if(!error.value){if(form.value==='group')selectedGroup.value=createdGroup;form.value=''}}}
async function confirmRemoveMember(u){try{await ElMessageBox.confirm(`移除 ${u.username} 后，其本租户会话和授权将撤销。`,'移除成员',{confirmButtonText:'移除',cancelButtonText:'取消',type:'warning'})}catch{return}await removeMember(u)}
async function confirmDisable(kind,row){try{await ElMessageBox.confirm(row.disabled?'恢复启用该对象？':'停用后相关会话将断开。确认停用？','更新状态',{confirmButtonText:'确认',cancelButtonText:'取消',type:'warning'})}catch{return}await changeDisabled(kind,row)}
async function deviceAction(cmd,d){
 error.value=''
 if(cmd==='groups')editGroups(d)
 if(cmd==='disable')await confirmDisable('devices',d)
 if(cmd==='delete'){
  try{await ElMessageBox.confirm(`软删除 ${d.wire_id} 后将从列表隐藏、断开设备及会话，并永久作废当前 Token。审计记录和设备编号仍保留，不能直接重新登记同一编号。`,'软删除设备',{confirmButtonText:'确认软删除',cancelButtonText:'取消',type:'warning'})}catch{return}
  await action(()=>axios.delete(`/api/manage/devices/${d.id}`))
 }
 if(cmd==='rotate'){
  try{
   await ElMessageBox.confirm(`重置 ${d.wire_id} 的 Token 后，当前连接会断开，旧 Token 永久失效。`,'重置 Token · 第一步确认',{confirmButtonText:'继续确认',cancelButtonText:'取消',type:'warning'})
   await ElMessageBox.prompt(`请再次输入设备编号 ${d.wire_id}。完成重置后必须将新 Token 配置到客户端。`,'重置 Token · 最终确认',{confirmButtonText:'确认重置',cancelButtonText:'取消',inputPlaceholder:'输入完整设备编号',inputValidator:value=>value===d.wire_id||'设备编号不一致',type:'warning'})
  }catch{return}
  await rotate(d)
 }
}
function groupAction(cmd){if(cmd==='rename')Object.assign(renameGroup,{old:selectedGroup.value,name:selectedGroup.value,open:true});else if(cmd==='delete')deleteGroup({name:selectedGroup.value})}
async function revokeGrant(row){Object.assign(grant,{user_id:row.user_id,device_id:row.device_id,group:row.group});await changeGrant(false)}
</script>
<style src="../styles/console.css"></style>

<style scoped>
.tenant-feature-tags{display:flex;flex-wrap:wrap;gap:5px}.tenant-feature-tags span{font-size:11px;padding:3px 7px;border:1px solid #dde4f2;background:#f4f6fc;color:#667aaa;border-radius:4px}.tenant-feature-tags span.off{color:#a2a9b5;background:#f6f7f9;border-color:#eceef2;text-decoration:line-through}.feature-options{display:flex;flex-direction:column;gap:12px;margin:24px 0}.feature-options label{display:flex;align-items:center;justify-content:space-between;gap:18px;padding:18px 16px;border:1px solid #e5e9f1;border-radius:8px}.feature-options b{font-size:13px;font-weight:550;display:block;color:#4c5e79}.feature-options small{display:block;color:#8a97aa;font-size:11px;line-height:1.8;margin-top:6px}
</style>

-- =============================================================================
-- luci-app-cfdmgrd — cfdmgrd 的 OpenWrt LuCI「web 壳子」控制器
--
--   在路由器后台网页里：配端口/令牌、下载/更新核心二进制 cfdmgrd、启停服务、看
--   版本状态、一键打开 cfdmgrd 自带的隧道管理后台（:端口）。
--
--   复用已装好的 /usr/sbin/cfdmgrd-fetch（架构检测 + 自建源优先下载 + 安装）、
--   /etc/config/cfdmgrd（UCI）、/etc/init.d/cfdmgrd（procd）。
--   cloudflared 连接器二进制由守护进程自身的二进制自动更新负责，不在此处理。
-- =============================================================================
module("luci.controller.cfdmgr", package.seeall)

local sys  = require "luci.sys"
local http = require "luci.http"
local util = require "luci.util"
local fs   = require "nixio.fs"
local uci  = require "luci.model.uci".cursor()

local BIN     = "/usr/bin/cfdmgrd"
local INIT    = "/etc/init.d/cfdmgrd"
local FETCH   = "/usr/sbin/cfdmgrd-fetch"
local DL_LOG  = "/tmp/cfdmgrd-fetch.log"
local DL_LOCK = "/tmp/cfdmgrd-fetch.running"

function index()
	if not fs.access("/etc/config/cfdmgrd") then return end
	entry({"admin", "services", "cfdmgr"},
		template("cfdmgr/main"), _("Cloudflared Manager"), 60).dependent = true
	entry({"admin", "services", "cfdmgr", "info"},            call("action_info"))
	entry({"admin", "services", "cfdmgr", "save"},            call("action_save"))
	entry({"admin", "services", "cfdmgr", "download"},        call("action_download"))
	entry({"admin", "services", "cfdmgr", "download_status"}, call("action_download_status"))
	entry({"admin", "services", "cfdmgr", "control"},         call("action_control")).leaf = true
end

local function u(k, d)
	local v = uci:get("cfdmgrd", "main", k)
	if v == nil or v == "" then return d end
	return v
end

local function is_running()
	return sys.call("pidof cfdmgrd >/dev/null 2>&1") == 0
end

-- 运行信息：架构、是否已装二进制、版本、运行状态、当前配置
function action_info()
	local has = fs.access(BIN) and true or false
	local ver = ""
	if has then ver = util.trim(sys.exec(util.shellquote(BIN) .. " version 2>/dev/null")) end
	http.prepare_content("application/json")
	http.write_json({
		arch        = util.trim(sys.exec("uname -m 2>/dev/null")),
		has_binary  = has,
		version     = ver,
		running     = is_running(),
		downloading = fs.access(DL_LOCK) and true or false,
		cfg = {
			enabled        = u("enabled", "1"),
			http_addr      = u("http_addr", ":18080"),
			token          = u("token", ""),
			data_dir       = u("data_dir", "/usr/lib/cfdmgrd"),
			log_level      = u("log_level", "info"),
			version        = u("version", ""),
			self_update    = u("self_update", "0"),
			cfd_autoupdate = u("cfd_autoupdate", "1"),
		},
	})
end

-- 保存配置（端口/令牌/数据目录/日志级别/拉取版本/自更新开关）
function action_save()
	local function setv(opt, val, allow_empty)
		if val == nil then return end
		if val == "" and not allow_empty then return end
		uci:set("cfdmgrd", "main", opt, val)
	end
	-- 确保 section 存在
	if not uci:get("cfdmgrd", "main") then
		uci:set("cfdmgrd", "main", "cfdmgrd")
	end
	local http_addr = http.formvalue("http_addr")
	if http_addr ~= nil and http_addr ~= "" then
		-- 允许 :端口 或 ip:端口
		uci:set("cfdmgrd", "main", "http_addr", http_addr)
	end
	setv("token",     http.formvalue("token"),     false)
	setv("data_dir",  http.formvalue("data_dir"),  false)
	setv("log_level", http.formvalue("log_level"), false)
	setv("version",   http.formvalue("version"),   true)
	local en = http.formvalue("enabled")
	if en == "1" or en == "0" then uci:set("cfdmgrd", "main", "enabled", en) end
	local su = http.formvalue("self_update")
	if su == "1" or su == "0" then uci:set("cfdmgrd", "main", "self_update", su) end
	local ca = http.formvalue("cfd_autoupdate")
	if ca == "1" or ca == "0" then uci:set("cfdmgrd", "main", "cfd_autoupdate", ca) end
	uci:commit("cfdmgrd")
	http.prepare_content("application/json")
	http.write_json({ ok = true })
end

-- 异步下载/更新核心：后台跑 cfdmgrd-fetch，日志写 DL_LOG，锁文件标识进行中
function action_download()
	http.prepare_content("application/json")
	if fs.access(DL_LOCK) then
		http.write_json({ ok = true, status = "in_progress" })
		return
	end
	local version = http.formvalue("version") or ""   -- 可空（用 UCI/随包版本）或 latest 或具体版本
	local verarg = ""
	if version ~= "" then verarg = " " .. util.shellquote(version) end
	-- 包一层：建锁 -> 跑 fetch（输出进日志）-> 删锁；整体后台化
	local cmd = string.format(
		"( touch %s; %s%s > %s 2>&1; rm -f %s ) >/dev/null 2>&1 &",
		util.shellquote(DL_LOCK), util.shellquote(FETCH), verarg,
		util.shellquote(DL_LOG), util.shellquote(DL_LOCK))
	sys.call(cmd)
	http.write_json({ ok = true, status = "started" })
end

-- 下载进度/结果：是否仍在跑、日志尾部、是否已装好、版本
function action_download_status()
	http.prepare_content("application/json")
	local has = fs.access(BIN) and true or false
	local ver = ""
	if has then ver = util.trim(sys.exec(util.shellquote(BIN) .. " version 2>/dev/null")) end
	local logtail = ""
	if fs.access(DL_LOG) then
		logtail = sys.exec("tail -n 200 " .. util.shellquote(DL_LOG) .. " 2>/dev/null")
	end
	http.write_json({
		running    = fs.access(DL_LOCK) and true or false,
		has_binary = has,
		version    = ver,
		log        = logtail,
	})
end

-- 服务控制：start/stop/restart/enable/disable
function action_control(act)
	http.prepare_content("application/json")
	local allow = { start = true, stop = true, restart = true, enable = true, disable = true }
	if not allow[act] then
		http.write_json({ ok = false, error = "invalid action" })
		return
	end
	local rc = sys.call(INIT .. " " .. act .. " >/dev/null 2>&1")
	http.write_json({ ok = (rc == 0), running = is_running() })
end

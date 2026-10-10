let map = null;
let candidateMarker = null;
let activeMarker = null;
let mapConfig = null;
let mapUsable = false;
let sessionToken = '';
let lastStatus = null;
let connected = false;
let busy = false;
let statusRequest = null;
let previewVersion = 0;
let previewTimer = null;

const dom = {
    deviceBadge: document.getElementById('device-badge'),
    alertBox: document.getElementById('alert-box'),
    alertMessage: document.getElementById('alert-message'),
    btnRecover: document.getElementById('btn-recover'),
    infoDevice: document.getElementById('info-device'),
    infoState: document.getElementById('info-state'),
    infoActiveCoords: document.getElementById('info-active-coords'),
    infoLastTime: document.getElementById('info-last-time'),
    coordSys: document.getElementById('coord-sys'),
    inputLat: document.getElementById('input-lat'),
    inputLng: document.getElementById('input-lng'),
    btnStart: document.getElementById('btn-start'),
    btnApply: document.getElementById('btn-apply'),
    btnStop: document.getElementById('btn-stop'),
    mapFallback: document.getElementById('map-fallback'),
    mapMessage: document.getElementById('map-message'),
    result: document.getElementById('action-result')
};

async function api(path, body, timeout = 35000) {
    const options = { signal: AbortSignal.timeout(timeout), cache: 'no-store' };
    if (body !== undefined) {
        options.method = 'POST';
        options.headers = { 'Content-Type': 'application/json', 'X-Session-Token': sessionToken };
        options.body = JSON.stringify(body);
    }
    const response = await fetch(path, options);
    const data = await response.json();
    if (!response.ok || data.success === false) {
        throw new Error(data.message || '请求失败: ' + response.status);
    }
    return data;
}

function showMapMessage(message) {
    dom.mapMessage.textContent = message;
    dom.mapFallback.classList.remove('hidden');
}

function initMap(config, message) {
    mapConfig = config;
    if (!config.tile_url || message) {
        showMapMessage(message || '未配置地图，可直接输入坐标');
        return;
    }
    if (typeof L === 'undefined') {
        showMapMessage('地图组件未加载，可直接输入坐标');
        return;
    }
    if (!['WGS-84', 'GCJ-02'].includes(config.coordinate_system)) {
        showMapMessage('底图坐标系无效，可直接输入 WGS-84 坐标');
        return;
    }
    try {
        map = L.map('map', {
            center: [39.9087, 116.3975],
            zoom: Math.max(config.min_zoom, Math.min(13, config.max_zoom)),
            zoomControl: true
        });
        let loaded = 0;
        let failed = 0;
        const layer = L.tileLayer(config.tile_url, {
            subdomains: config.subdomains || 'abc',
            minZoom: config.min_zoom,
            maxZoom: config.max_zoom,
            attribution: config.attribution
        });
        layer.on('loading', () => { loaded = 0; failed = 0; });
        layer.on('tileload', () => {
            loaded += 1;
            mapUsable = true;
            dom.mapFallback.classList.add('hidden');
        });
        layer.on('tileerror', () => {
            failed += 1;
            if (loaded === 0) {
                mapUsable = false;
                showMapMessage('地图瓦片加载失败，可直接输入坐标');
            }
        });
        layer.on('load', () => {
            if (loaded === 0 && failed > 0) {
                mapUsable = false;
                showMapMessage('地图服务暂不可用，可直接输入坐标');
            }
        });
        layer.addTo(map);
        map.on('click', (event) => {
            if (!mapUsable) return;
            const point = event.latlng.wrap();
            dom.inputLat.value = point.lat.toFixed(6);
            dom.inputLng.value = point.lng.toFixed(6);
            dom.coordSys.value = config.coordinate_system;
            previewVersion += 1;
            clearTimeout(previewTimer);
            updateCandidateMarker(point.lat, point.lng);
        });
    } catch (error) {
        mapUsable = false;
        showMapMessage('地图初始化失败：' + error.message + '。可直接输入坐标');
    }
}

function updateCandidateMarker(lat, lng) {
    if (!map) return;
    if (candidateMarker) candidateMarker.setLatLng([lat, lng]);
    else candidateMarker = L.marker([lat, lng], { title: '候选目标点' })
        .addTo(map).bindPopup('候选位置，点击开始或应用后发送');
}

function updateActiveMarker(lat, lng) {
    if (!map) return;
    if (activeMarker) activeMarker.setLatLng([lat, lng]);
    else activeMarker = L.circleMarker([lat, lng], {
        radius: 8, color: '#10b981', fillColor: '#34d399', fillOpacity: 0.8
    }).addTo(map).bindPopup('最近成功发送的位置');
}

function readLocation() {
    if (dom.inputLat.value.trim() === '' || dom.inputLng.value.trim() === '') {
        throw new Error('请输入经纬度，或在地图上选点');
    }
    const latitude = Number(dom.inputLat.value);
    const longitude = Number(dom.inputLng.value);
    if (!Number.isFinite(latitude) || latitude < -90 || latitude > 90 ||
        !Number.isFinite(longitude) || longitude < -180 || longitude > 180) {
        throw new Error('纬度范围为 -90～90，经度范围为 -180～180');
    }
    return { latitude, longitude, coordinate_system: dom.coordSys.value };
}

function queuePreview() {
    const version = ++previewVersion;
    clearTimeout(previewTimer);
    if (candidateMarker && map) {
        map.removeLayer(candidateMarker);
        candidateMarker = null;
    }
    if (!map || !sessionToken) return;
    previewTimer = setTimeout(async () => {
        try {
            const point = await api('/api/preview', readLocation(), 7000);
            if (version === previewVersion) updateCandidateMarker(point.latitude, point.longitude);
        } catch (_) {
            // Incomplete input is normal while typing; start/apply validates explicitly.
        }
    }, 200);
}

function updateButtons() {
    const state = lastStatus?.state;
    const locked = busy || !connected || !sessionToken;
    dom.btnStart.disabled = locked || state !== '就绪';
    dom.btnApply.disabled = locked || state !== '模拟中';
    dom.btnStop.disabled = locked || state !== '模拟中';
    dom.btnRecover.disabled = locked || state !== '待清理';
}

async function fetchStatus() {
    if (statusRequest) return statusRequest;
    statusRequest = (async () => {
        try {
            const data = await api('/api/status', undefined, 8000);
            connected = true;
            lastStatus = data;
            renderStatus(data);
        } catch (_) {
            connected = false;
            dom.deviceBadge.className = 'badge badge-unavailable';
            dom.deviceBadge.textContent = '连接中断';
            dom.infoState.textContent = '无法连接本地服务，手机状态未知';
            dom.infoLastTime.textContent = '连接中断，无法确认是否仍在注入';
        } finally {
            updateButtons();
        }
    })();
    try { await statusRequest; } finally { statusRequest = null; }
}

function renderStatus(data) {
    dom.infoState.textContent = data.state;
    dom.infoDevice.textContent = data.device ?
        `${data.device.model || 'Android'} (${data.device.serial})` : '无就绪设备';
    const badges = {
        '就绪': ['ready', '设备就绪'],
        '模拟中': ['running', '正在模拟'],
        '待清理': ['pending', '待恢复清理'],
        '启动中': ['pending', '启动中'],
        '停止中': ['pending', '停止中']
    };
    const badge = badges[data.state] || ['unavailable', data.state || '不可用'];
    dom.deviceBadge.className = 'badge badge-' + badge[0];
    dom.deviceBadge.textContent = badge[1];

    if (data.last_success_time) {
        dom.infoActiveCoords.textContent = `${data.target_lat.toFixed(6)}, ${data.target_lng.toFixed(6)}`;
        if (Number.isFinite(data.display_lat) && Number.isFinite(data.display_lng)) {
            updateActiveMarker(data.display_lat, data.display_lng);
        }
        const age = Date.now() - Date.parse(data.last_success_time);
        const suffix = data.state === '模拟中' && age < 8000 ? '最近发送成功' : '历史记录，非当前状态保证';
        dom.infoLastTime.textContent = new Date(data.last_success_time).toLocaleTimeString() + ' · ' + suffix;
    } else {
        dom.infoActiveCoords.textContent = '尚无成功发送记录';
        dom.infoLastTime.textContent = '-';
        if (activeMarker && map) { map.removeLayer(activeMarker); activeMarker = null; }
    }
    const pending = data.state === '待清理';
    dom.alertBox.classList.toggle('hidden', !data.error_message && !pending);
    dom.alertMessage.textContent = data.error_message || (pending ? '存在遗留会话需要清理' : '');
    dom.btnRecover.classList.toggle('hidden', !pending);
    updateButtons();
}

async function performAction(path, needsLocation = false) {
    if (busy) return;
    let body = {};
    try { if (needsLocation) body = readLocation(); }
    catch (error) { dom.result.textContent = error.message; return; }
    busy = true;
    dom.result.textContent = '正在处理…';
    updateButtons();
    try {
        const result = await api(path, body);
        dom.result.textContent = result.message || '操作完成';
    } catch (error) {
        dom.result.textContent = error.message + '；请查看当前状态，必要时执行清理恢复。';
    } finally {
        // Wait for an older poll before asking for the post-operation state.
        if (statusRequest) await statusRequest;
        await fetchStatus();
        busy = false;
        updateButtons();
    }
}

dom.btnStart.addEventListener('click', () => performAction('/api/start', true));
dom.btnApply.addEventListener('click', () => performAction('/api/location', true));
dom.btnStop.addEventListener('click', () => performAction('/api/stop'));
dom.btnRecover.addEventListener('click', () => performAction('/api/recover'));
for (const input of [dom.inputLat, dom.inputLng, dom.coordSys]) {
    input.addEventListener('input', queuePreview);
    input.addEventListener('change', queuePreview);
}

window.addEventListener('DOMContentLoaded', async () => {
    updateButtons();
    try {
        const config = await api('/api/bootstrap', undefined, 8000);
        sessionToken = config.token;
        initMap(config.map, config.message);
    } catch (error) {
        showMapMessage('初始化失败，请刷新页面：' + error.message);
        dom.result.textContent = '无法建立本地会话，请确认工具正在运行后刷新页面。';
    }
    async function poll() {
        await fetchStatus();
        setTimeout(poll, 1000);
    }
    await poll();
});

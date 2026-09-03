class BLE extends DeviceService
{
    static serviceName = 'ble';
    static shortName = 'ble';

    permitJoin = false;

    constructor(controller, instance)
    {
        super(controller, 'ble', instance);
        this.intervals.push(setInterval(function() { this.updateLastSeen(); }.bind(this), 100));
    }

    updateLastSeen()
    {
        if (this.controller.service != this.service)
            return;

        Object.keys(this.devices).forEach(id =>
        {
            let value = timeInterval(Date.now() / 1000 - this.devices[id].lastSeen);
            
            // Обновление в списке устройств
            let cell = document.querySelector('tr[data-device="' + this.service + '/' + id + '"] .lastSeen');
            if (cell && cell.innerHTML != value) {
                cell.dataset.value = this.devices[id].lastSeen;
                cell.innerHTML = value;
            }
            
            // Обновление на странице информации об устройстве
            let infoCell = document.querySelector('.deviceInfo .lastSeen');
            if (infoCell && this.device && this.device.id == id && infoCell.innerHTML != value) {
                infoCell.innerHTML = value;
                infoCell.classList.remove('right');
                infoCell.classList.add('left');
            }
        });
    }

updatePage()
    {
        document.querySelector('#serviceVersion').innerHTML = this.version ? 'BLE ' + this.version : '<i>unknown</i>';
        document.querySelector('#permitJoin i').className = 'icon-enable ' + (this.permitJoin ? 'warning' : 'shade');
    }

    parseMessage(list, message)
    {
        if (list[0] == 'status')
        {
            let check = Object.keys(this.devices).length ? false : true;

            this.names = message.names;
            this.version = message.version;

            message.devices.forEach(device =>
            {
                if (!device.name)
                    device.name = device.id;

                if (!this.devices[device.id])
                {
                    let topics = [device.id];
                    if (device.name && device.name !== device.id)
                        topics.push(device.name);

                    this.devices[device.id] = new Device(this.service, device.id);
                    topics.forEach(topic =>
                    {
                        this.controller.socket.subscribe('expose/' + this.service + '/' + topic);
                        this.controller.socket.subscribe('device/' + this.service + '/' + topic);
                    });

                    check = true;
                }

                this.devices[device.id].info = device;

                if (this.controller.service != this.service || this.devices[device.id] != this.device)
                    return;

                this.showDeviceInfo(this.device);
            });

            Object.keys(this.devices).forEach(id =>
            {
                if (message.devices.filter(device => device.id == id).length)
                    return;

                delete this.devices[id];
                check = true;
            });

            if (this.controller.service == this.service)
            {
                if (check)
                    this.controller.showPage(this.service);

                this.updatePage();
            }

            return;
        }

        super.parseMessage(list, message);
    }

showPage(data)
    {
        let menu = document.querySelector('.menu');
        let list = data ? data.split('=') : new Array();
        let device;

        menu.innerHTML  = '<span id="list"><i class="icon-list"></i> List</span>';
        menu.innerHTML += '<span id="add"><i class="icon-plus"></i> Add</span>';
        menu.innerHTML += '<span id="import" class="mobileHidden"><i class="icon-upload"></i> Import</span>';
        menu.innerHTML += '<span id="permitJoin"><i class="icon-enable"></i> Permit Join</span>';

menu.querySelector('#list').addEventListener('click', function() { this.controller.showPage(this.service); }.bind(this));
        menu.querySelector('#add').addEventListener('click', function() { this.showDeviceEdit(); }.bind(this));
        menu.querySelector('#permitJoin').addEventListener('click', function() { menu.querySelector('#permitJoin i').className = 'icon-enable'; this.serviceCommand({action: 'togglePermitJoin'}); }.bind(this));

        menu.querySelector('#import').addEventListener('click', function()
        {
            loadFile(function(data)
            {
                let random = randomString(4);
                this.serviceCommand({action: 'updateDevice', data: {cloud: false, discovery: false, name: 'Device ' + random, id: 'device_' + random, ...data}});

            }.bind(this));

        }.bind(this));

        if (list[0] == 'device')
            device = this.devices[list[1]];

        if (device)
            this.showDeviceInfo(device);
        else
            this.showDeviceList();

        this.device = device;
        this.updatePage();
    }

    showDeviceInfo(device)
    {
        loadHTML('deviceInfo.html', this, this.content, function()
        {
            let table;

            this.content.querySelector('.name').innerHTML = device.info.name;
            this.content.querySelector('.edit').addEventListener('click', function() { this.showDeviceEdit(device); }.bind(this));
            this.content.querySelector('.remove').addEventListener('click', function() { this.showDeviceRemove(device); }.bind(this));

            this.updateDeviceInfo(device);
            this.updateArrowButtons(device);

            if (!device.info.active)
            {
                this.content.querySelector('.exposes').style.display = 'none';
                return;
            }

            table = this.content.querySelector('table.exposes');
            Object.keys(device.endpoints).forEach(endpointId => { device.items(endpointId).forEach(expose => { addExpose(table, device, endpointId, expose); }); });

            this.content.querySelector('.export').addEventListener('click', function()
            {
                let data = {exposes: device.info.exposes, real: device.info.real};
                let item = document.createElement("a");

                if (device.info.options)
                    data.options = device.info.options;

                if (device.info.bindings)
                    data.bindings = device.info.bindings;

                if (device.info.availabilityTopic)
                    data.availabilityTopic = device.info.availabilityTopic;

                if (device.info.availabilityPattern)
                    data.availabilityPattern = device.info.availabilityPattern;

                item.href = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], {type: 'application/json'}));
                item.download = device.info.name + '.json';
                item.click();

            }.bind(this));
        });
    }

    showDeviceRemove(device)
    {
        loadHTML('deviceRemove.html', this, modal.querySelector('.data'), function()
        {
            modal.querySelector('.name').innerHTML = device.info.name;
            modal.querySelector('.remove').addEventListener('click', function() { this.serviceCommand({action: 'removeDevice', device: this.names ? device.info.name : device.id}, true); }.bind(this));
            modal.querySelector('.cancel').addEventListener('click', function() { showModal(false); });
            showModal(true);
        });
    }

    showDeviceList()
    {
        if (!Object.keys(this.devices).length)
        {
            this.content.innerHTML = '<div class="emptyList">' + this.service + ' devices list is empty</div>';
            return;
        }

        loadHTML('deviceList.html', this, this.content, function()
        {
            let table = this.content.querySelector('.deviceList table');

            Object.keys(this.devices).forEach(id =>
            {
                let device = this.devices[id];
                let row = table.querySelector('tbody').insertRow();

                row.dataset.device = this.service + '/' + device.id;
                row.addEventListener('click', function() { this.controller.showPage(this.service + '?device=' + device.id); }.bind(this));

                for (let i = 0; i < 7; i++)
                {
                    let cell = row.insertCell();

                    switch (i)
                    {
                        case 0:

                            cell.innerHTML = device.info.name;
                            cell.colSpan = 2;

                            if (device.info.note)
                                row.title = device.info.note;

                            break;

                        case 1: cell.innerHTML = device.id; cell.classList.add('mobileHidden'); break;
                        case 2:
                        {
                            let exposeCount = Array.isArray(device.info.exposes) ? device.info.exposes.length : 0;
                            cell.innerHTML = '<span class="value">' + exposeCount + '</span>';
                            cell.classList.add('center');
                            break;
                        }
                        case 3: cell.innerHTML = this.parseValue(device.info, 'real'); cell.classList.add('center'); break;
                        case 4: cell.innerHTML = this.parseValue(device.info, 'discovery'); cell.classList.add('center', 'mobileHidden'); break;
                        case 5: cell.innerHTML = this.parseValue(device.info, 'cloud'); cell.classList.add('center', 'mobileHidden'); break;
                        case 6: cell.innerHTML = empty; cell.classList.add('lastSeen', 'right'); break;
                    }
                }
            });

            table.querySelectorAll('th.sort').forEach(cell => cell.addEventListener('click', function() { sortTable(table, this.dataset.index); localStorage.setItem('homedBLESort', this.dataset.index); }) );
            sortTable(table, localStorage.getItem('homedBLESort') ?? 0);
            addTableSearch(table, 'devices', 'device', 7, [0, 1]);
        });
    }

    showDeviceEdit(device)
    {
        let add = false;

        if (!device)
        {
            let random = randomString(4);
            device = {info: {name: 'Device ' + random, id: 'device_' + random, exposes: ['switch'], active: true}};
            add = true;
        }

        loadHTML('deviceEdit.html', this, modal.querySelector('.data'), function()
        {
            modal.querySelector('.name').innerHTML = device.info.name;
            modal.querySelector('input[name="name"]').value = device.info.name;
            modal.querySelector('textarea[name="note"]').value = device.info.note ?? '';
            modal.querySelector('input[name="id"]').value = device.info.id;
            modal.querySelector('input[name="exposes"]').value = device.info.exposes.join(', ');
            modal.querySelector('textarea[name="options"]').value = device.info.options ? JSON.stringify(device.info.options, null, 2) : '';

            modal.querySelector('.real').style.display = device.info.real ? 'block' : 'none';
            modal.querySelector('textarea[name="bindings"]').value = device.info.bindings ? JSON.stringify(device.info.bindings, null, 2) : '';
            modal.querySelector('input[name="availabilityTopic"]').value = device.info.availabilityTopic ?? '';
            modal.querySelector('textarea[name="availabilityPattern"]').value = device.info.availabilityPattern ?? '';

            modal.querySelector('input[name="real"]').checked = device.info.real;
            modal.querySelector('input[name="real"]').addEventListener('click', function() { modal.querySelector('.real').style.display = this.checked ? 'block' : 'none'; });

            modal.querySelector('input[name="discovery"]').checked = device.info.discovery;
            modal.querySelector('input[name="cloud"]').checked = device.info.cloud;
            modal.querySelector('input[name="active"]').checked = device.info.active;

            modal.querySelector('.save').addEventListener('click', function()
            {
                let form = formData(modal.querySelector('form'));

                if (form.exposes)
                    form.exposes = form.exposes.split(',').map(item =>item.trim());
                else
                    delete form.exposes;

                if (form.options)
                    try { form.options = JSON.parse(form.options); } catch { modal.querySelector('textarea[name="options"]').classList.add('error'); return; }
                else
                    delete form.options;

                if (form.bindings)
                    try { form.bindings = JSON.parse(form.bindings); } catch { modal.querySelector('textarea[name="bindings"]').classList.add('error'); return; }
                else
                    delete form.bindings;

                this.serviceCommand({action: 'updateDevice', device: add ? null : this.names ? device.info.name : device.id, data: form});

            }.bind(this));

            modal.querySelector('textarea[name="options"]').addEventListener('input', function() { this.classList.remove('error'); });
            modal.querySelector('textarea[name="bindings"]').addEventListener('input', function() { this.classList.remove('error'); });
            modal.querySelector('.cancel').addEventListener('click', function() { showModal(false); });
            showModal(true, 'input[name="name"]');
        });
    }
}

registerPlugin(BLE);

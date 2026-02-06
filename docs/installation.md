### Installation
Create a venv on Raspberry:  
`python -m venv venv`  

Activate the venv on Raspberry:  
`source venv/bin/activate`  

Activate the venv on Windows:  
`venv\Scripts\activate`  

Install dependencies:  
`pip install -r requirements.txt`  

### Autostart
Create a file selbst-ableser.service  
`sudo nano /etc/systemd/system/selbst-ableser.service`  

```
[Unit]
Description=Read wmbus meters
After=network.target  

[Service]  
ExecStart=/home/sf/selbst-ableser/venv/bin/python /home/sf/selbst-ableser/main.py  
WorkingDirectory=/home/sf/selbst-ableser/
StandardOutput=inherit
StandardError=inherit
Restart=always
RestartSec=10
User=sf

[Install]
WantedBy=multi-user.target
```

Start the service and enable it to be started at every boot:  
`sudo systemctl daemon-reload`  
`sudo systemctl start selbst-ableser.service`  
`sudo systemctl enable selbst-ableser.service`  

Check status of service  
`sudo systemctl status selbst-ableser.service`  

Check, which services are enabled  
`systemctl list-unit-files --state=enabled`  

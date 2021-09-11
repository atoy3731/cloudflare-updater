import requests
import os
import json
import logging
import sys
import time

log = logging.getLogger()

handler = logging.StreamHandler(sys.stdout)
handler.setLevel(logging.DEBUG)
formatter = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')
handler.setFormatter(formatter)
log.addHandler(handler)

IP_URL = os.environ.get('IP_URL', 'https://checkip.amazonaws.com/')
IP_FILE = '/tmp/ip'

CLOUDFLARE_TOKEN = os.environ.get('CLOUDFLARE_TOKEN')
CLOUDFLARE_ZONE = os.environ.get('CLOUDFLARE_ZONE', 'example.com')
CLOUDFLARE_RECORD = os.environ.get('CLOUDFLARE_RECORD', 'www.example.com')
CLOUDFLARE_DNS_TTL = int(os.environ.get('CLOUDFLARE_DNS_TTL', '1'))

INTERVAL_MINS = int(os.environ.get('INTERVAL_MINS', '5'))

LOG_LEVEL = os.environ.get('LOG_LEVEL', 'info')

if LOG_LEVEL == 'info':
    log.setLevel(logging.INFO)
if LOG_LEVEL == 'debug':
    log.setLevel(logging.DEBUG)
if LOG_LEVEL == 'warning':
    log.setLevel(logging.WARNING)

def _create_ip_file():
    """
    Creates the file to hold the IP if it doesn't exist.
    :return: N/A
    """
    if not os.path.isfile(IP_FILE):
        log.debug("Creating IP file '{}'".format(IP_FILE))
        with open(IP_FILE, 'w') as file:
            pass

def _get_ip():
    """
    Retrieves current IP.
    If using a custom URL, the response needs to be plain-text IP.
    :return: (str) IP Address
    """
    log.debug('Acquiring current IP..')
    resp = requests.get(IP_URL)
    ip = resp.text.strip()
    log.debug('Current IP: {}'.format(ip))
    return ip

def _is_updated_ip(ip):
    with open(IP_FILE, 'r') as file:
        existing_ip = file.read().strip()
        if existing_ip == '':
            existing_ip = 'N/A'

    if existing_ip != ip:
        log.info('IP Updated ({} -> {})'.format(existing_ip, ip))
        with open(IP_FILE, 'w') as file:
            file.write(ip)
        return True
    else:
        log.debug('No IP change ({})'.format(ip))

    return False

def _update_cloudflare_record(current_ip):
    """
    Updates DNS record in Cloudflare to current IP.
    :param current_ip: Current public IP
    :return: N/A
    """
    headers = {
        'Authorization': str('Bearer {}'.format(CLOUDFLARE_TOKEN)).strip()
    }

    zone_id = None
    record_id = None

    # Get zone ID
    log.debug('Getting Cloudflare Zone ID')
    resp = requests.get('https://api.cloudflare.com/client/v4/zones?name={}&status=active'.format(CLOUDFLARE_ZONE), headers=headers)
    if resp.status_code == 403:
        log.error('[ERROR] Failed auth with Cloudflare. Check your token!')
        return None
    elif resp.status_code == 200:
        obj = json.loads(resp.text)
        if len(obj['result']) == 0:
            log.error("[ERROR] Could not find valid CloudFlare Zone ID for '{}'".format(CLOUDFLARE_ZONE))
            return None
        else:
            zone_id = obj['result'][0]['id']

    # Get record ID
    log.debug('Getting Cloudflare Record ID')
    resp = requests.get('https://api.cloudflare.com/client/v4/zones/{}/dns_records?type=A&name={}'.format(zone_id, CLOUDFLARE_RECORD), headers=headers)
    if resp.status_code == 403:
        print('[ERROR] Failed auth with Cloudflare. Check your token!')
        return None
    elif resp.status_code == 200:
        obj = json.loads(resp.text)
        if len(obj['result']) == 0:
            log.error("[ERROR] Could not find valid CloudFlare DNS Record ID for '{}'".format(CLOUDFLARE_RECORD))
            return None
        else:
            record_id = obj['result'][0]['id']

    # Update record with IP
    data = {
        'type': 'A',
        'name': CLOUDFLARE_RECORD,
        'content': current_ip,
        'ttl': CLOUDFLARE_DNS_TTL,
        'proxied': False
    }

    log.debug('Updating Cloudflare Record ID')
    resp = requests.put('https://api.cloudflare.com/client/v4/zones/{}/dns_records/{}'.format(zone_id, record_id),
                        headers=headers,
                        data=json.dumps(data)
                )

    if resp.status_code == 403:
        log.error('[ERROR] Failed auth with Cloudflare. Check your token!')
        return None
    elif resp.status_code != 200:
        log.error("[ERROR] Could not update DNS Record ID for '{}'".format(CLOUDFLARE_RECORD))
        log.error("  -> {}".format(resp.text))
    else:
        log.info("Record updated successfully! ({} -> {})".format(CLOUDFLARE_RECORD, current_ip))


if __name__ == '__main__':
    log.info('========================')
    log.info(' Cloudflare DNS Updater ')
    log.info('========================')
    log.info('')
    log.info('Running every {} minutes'.format(INTERVAL_MINS))

    _create_ip_file()

    while True:
        current_ip = _get_ip()
        if _is_updated_ip(current_ip):
            _update_cloudflare_record(current_ip)

        time.sleep(INTERVAL_MINS * 60)

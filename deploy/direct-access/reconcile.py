"""Root-only timer. Owns only priority -35, exact-source TCP/443 accept rules."""
import fcntl
import logging
import os
import re
import subprocess

import psycopg

from policy import firewall_rule, public_ip

RULE = re.compile(r'^rule priority="-35" family="ipv[46]" source address="([0-9a-fA-F:.]+)" port port="443" protocol="tcp" accept$')


def fw(*args):
    return subprocess.check_output(['/usr/bin/firewall-cmd', '--zone=public', *args], text=True, timeout=20).strip()


def main():
    with open('/run/sub6-direct-reconcile.lock', 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        with psycopg.connect(os.environ['DATABASE_URL'], connect_timeout=5, options='-c statement_timeout=5000') as conn:
            wanted = {public_ip(r[0]) for r in conn.execute('SELECT DISTINCT host(ip) FROM direct_access.eligible_entries')}
        current = set()
        for line in fw('--list-rich-rules').splitlines():
            match = RULE.fullmatch(line)
            if match:
                current.add(public_ip(match[1]))
        for ip in sorted(current - wanted):
            fw('--remove-rich-rule=' + firewall_rule(ip))
        for ip in sorted(wanted - current):
            fw('--add-rich-rule=' + firewall_rule(ip))
        # Verify actual firewalld state before publishing the applied snapshot.
        actual = {public_ip(m[1]) for line in fw('--list-rich-rules').splitlines() if (m := RULE.fullmatch(line))}
        if actual != wanted:
            raise RuntimeError('firewall verification failed')
        with psycopg.connect(os.environ['DATABASE_URL'], connect_timeout=5, options='-c statement_timeout=5000') as conn:
            conn.execute('UPDATE direct_access.sync_state SET ips=%s,applied_at=now() WHERE id=1', (sorted(actual),))
        logging.info('synchronized %d unique IPs, added=%d removed=%d', len(actual), len(wanted-current), len(current-wanted))


if __name__ == '__main__':
    logging.basicConfig(level=logging.INFO)
    main()

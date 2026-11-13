"""No shell fragments or forwarded headers are accepted as an IP address."""
import ipaddress


def public_ip(value):
    if not isinstance(value, str) or len(value) > 45 or '/' in value or '%' in value:
        raise ValueError('只支持单个固定公网 IPv4 或 IPv6，不支持网段、端口和域名')
    try:
        ip = ipaddress.ip_address(value.strip())
    except ValueError:
        raise ValueError('IP 格式不正确') from None
    if not ip.is_global or ip.is_multicast or ip.is_unspecified or (isinstance(ip, ipaddress.IPv6Address) and ip.ipv4_mapped):
        raise ValueError('请填写可路由的公网 IP，不能填写内网、保留或映射地址')
    return str(ip)


def firewall_rule(ip):
    ip = public_ip(ip)
    family = 'ipv6' if ':' in ip else 'ipv4'
    return f'rule priority="-35" family="{family}" source address="{ip}" port port="443" protocol="tcp" accept'

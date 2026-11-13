import unittest
from policy import public_ip, firewall_rule


class PolicyTest(unittest.TestCase):
    def test_canonical_public(self):
        self.assertEqual(public_ip(' 9.9.9.9 '), '9.9.9.9')
        self.assertEqual(public_ip('2606:4700:4700:0:0:0:0:1111'), '2606:4700:4700::1111')

    def test_reject_nonpublic_and_shell(self):
        for value in [None, 123, '127.0.0.1', '10.0.0.1', '172.16.0.1', '192.168.1.1', '169.254.169.254', '100.64.0.1', '192.0.2.1', '203.0.113.10', '224.0.0.1', '255.255.255.255', '::', '::1', 'fc00::1', 'fe80::1', 'ff02::1', '2001:db8::1', '::ffff:9.9.9.9', '8.8.8.8/32', 'example.com', '8.8.8.8:443', '8.8.8.8;id', '8.8.8.8\naccept', 'fe80::1%eth0']:
            with self.subTest(value=value), self.assertRaises(ValueError):
                public_ip(value)

    def test_only_scoped_rule(self):
        self.assertEqual(firewall_rule('9.9.9.9'), 'rule priority="-35" family="ipv4" source address="9.9.9.9" port port="443" protocol="tcp" accept')


if __name__ == '__main__':
    unittest.main()

import { Avatar, Dropdown, Space, MenuProps } from 'antd';
import { useNavigate } from 'react-router-dom';
import styles from './index.module.scss';
import { UserOutlined, GlobalOutlined } from '@ant-design/icons';
import { useEffect, useState } from 'react';
import { jwtDecode } from 'jwt-decode';

interface DecodedToken {
    username: string;
    isPlatformAdmin: boolean;
    // JWT标准字段
    exp: number;
    iat: number;
}

const Toolbar = () => {
    const navigate = useNavigate();
    const [userInfo, setUserInfo] = useState<{ username: string; role: string }>({ username: '', role: '' });

    useEffect(() => {
        const token = localStorage.getItem('token');
        if (token) {
            try {
                const decoded = jwtDecode<DecodedToken>(token);
                setUserInfo({
                    username: decoded.username || '',
                    role: decoded.isPlatformAdmin ? '平台管理员' : '普通用户'
                });
            } catch (error) {
                console.error('Failed to decode token:', error);
            }
        }
    }, []);

    const handleLogout = () => {
        localStorage.removeItem("token");
        navigate('/login');
    };

    const menuItems: MenuProps['items'] = [
        {
            key: 'username',
            label: (
                <div style={{ padding: '4px 0', display: 'flex', alignItems: 'center', gap: '8px' }}>
                    <span>{userInfo.username}</span>
                    <span style={{
                        backgroundColor: '#f0f0f0',
                        padding: '2px 8px',
                        borderRadius: '4px',
                        fontSize: '12px'
                    }}>
                        {userInfo.role}
                    </span>
                </div>
            )
        },
        {
            key: 'divider-1',
            type: 'divider'
        },
        {
            key: "user_profile_login_settings",
            label: "登录设置",
            icon: <i className="fa-solid fa-key"></i>,
            onClick: () => navigate('/user/profile/login_settings')
        },
        {
            key: "user_profile_clusters",
            label: "我的集群",
            icon: <i className="fa-solid fa-server"></i>,
            onClick: () => navigate('/user/profile/my_clusters')
        },
        {
            key: 'divider-2',
            type: 'divider'
        },
        {
            key: 'logout',
            label: '退出',
            icon: <i className="fa-solid fa-right-from-bracket"></i>,
            onClick: handleLogout
        }
    ];

    const langOptions = [
        { label: '中文', value: 'chinese_simplified' },
        { label: 'EN', value: 'english' }
    ];

    const langMenu = {
        items: langOptions.map(opt => ({
            key: opt.value,
            label: (
                <span className='ignore' onClick={() => {
                    //@ts-ignore
                    if (window.translate && typeof window.translate.changeLanguage === 'function') {
                        //@ts-ignore
                        window.translate.changeLanguage(opt.value);
                    }
                }}>{opt.label}</span>
            )
        }))
    };

    return (
        <div className={styles.toolbar}>
            <Space>
                <Dropdown menu={{ items: menuItems }} placement='bottomRight'>
                    <span style={{ cursor: 'pointer' }}>
                        <Avatar size="small" style={{ backgroundColor: '#1677ff' }} >
                            <UserOutlined style={{ fontSize: 14 }} />
                        </Avatar>
                    </span>
                </Dropdown>
                <Dropdown menu={langMenu} placement="bottomRight" trigger={['click']}>
                    <Avatar size="small" style={{ backgroundColor: '#1677ff' }} >
                        <GlobalOutlined style={{ fontSize: 14 }} />
                    </Avatar>
                </Dropdown>
            </Space>
        </div >
    );
};

export default Toolbar;
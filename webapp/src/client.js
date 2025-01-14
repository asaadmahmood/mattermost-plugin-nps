import {id as pluginId} from './manifest';

const HEADER_X_CSRF_TOKEN = 'X-CSRF-Token';

export class Client {
    constructor() {
        this.csrf = getCSRFFromCookie();
        this.url = `/plugins/${pluginId}/api/v1`;
    }

    connected = () => {
        return this.doFetch(`${this.url}/connected`, {method: 'POST'});
    }

    doFetch = async (url, options) => {
        if (!options.headers) {
            options.headers = {};
        }

        options.headers['X-Requested-With'] = 'XMLHttpRequest';

        if (options.method && options.method.toLowerCase() !== 'get') {
            options.headers[HEADER_X_CSRF_TOKEN] = this.csrf;
        }

        try {
            const response = await fetch(url, options);
            const data = await response.json();

            return {data};
        } catch (error) {
            return {error};
        }
    }
}

function getCSRFFromCookie() {
    const cookies = document.cookie.split(';').map((cookie) => cookie.trim());

    for (const cookie of cookies) {
        if (cookie.startsWith('MMCSRF=')) {
            return cookie.replace('MMCSRF=', '');
        }
    }

    return '';
}

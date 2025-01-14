import * as ActionTypes from './action_types';

export function connected(client) {
    return () => {
        client.connected();
    };
}

export function showConfirmationModal(onConfirm, onCancel) {
    return {
        type: ActionTypes.SHOW_CONFIRMATION_MODAL,
        onCancel,
        onConfirm,
    };
}

export function hideConfirmationModal() {
    return {
        type: ActionTypes.HIDE_CONFIRMATION_MODAL,
    };
}

export function windowResized(windowWidth) {
    return {
        type: ActionTypes.WINDOW_RESIZED,
        windowWidth,
    };
}

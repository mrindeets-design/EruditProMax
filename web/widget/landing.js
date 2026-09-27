(function () {
    "use strict";

    const AVATAR_URL = "/widget/assets/erudit.png";
    const WELCOME_URL = "/widget/welcome.html";

    // =========================================================
    // УДАЛЯЕМ НАШИ СТАРЫЕ ВЕРСИИ
    // =========================================================

    const oldIds = [
        "erudit-launcher",
        "erudit-new-widget"
    ];

    oldIds.forEach(function (id) {
        const element = document.getElementById(id);

        if (element) {
            element.remove();
        }
    });


    // =========================================================
    // СОЗДАЁМ НОВЫЙ ВИДЖЕТ
    // =========================================================

    const host = document.createElement("div");

    host.id = "erudit-new-widget";

    host.style.position = "fixed";
    host.style.left = "0";
    host.style.top = "0";
    host.style.width = "100%";
    host.style.height = "100%";
    host.style.zIndex = "2147483647";
    host.style.pointerEvents = "none";

    document.body.appendChild(host);


    // =========================================================
    // SHADOW DOM
    // =========================================================

    const shadow = host.attachShadow({
        mode: "open"
    });


    // =========================================================
    // РАЗМЕТКА
    // =========================================================

    shadow.innerHTML = `

        <style>

            * {
                box-sizing: border-box;
            }


            /* =================================================
               КНОПКА
               ================================================= */

            #eruditCircle {

                position: fixed;

                right: 24px;
                bottom: 24px;

                width: 72px;
                height: 72px;

                padding: 0;

                display: flex;
                align-items: center;
                justify-content: center;

                border: 4px solid #ffffff;
                border-radius: 50%;

                background:
                    linear-gradient(
                        145deg,
                        #0a57aa,
                        #032f67
                    );

                box-shadow:
                    0 14px 40px
                    rgba(0, 43, 94, 0.35);

                cursor: pointer;

                pointer-events: auto;

                text-decoration: none;

                transition:
                    transform .2s ease,
                    box-shadow .2s ease;
            }


            #eruditCircle:hover {

                transform:
                    translateY(-3px)
                    scale(1.04);

                box-shadow:
                    0 18px 48px
                    rgba(0, 43, 94, 0.42);
            }


            #eruditCircle img {

                width: 60px;
                height: 60px;

                display: block;

                object-fit: contain;

                border-radius: 50%;
            }


            /* =================================================
               ЗЕЛЁНАЯ ТОЧКА
               ================================================= */

            #eruditOnline {

                position: absolute;

                right: -1px;
                bottom: -1px;

                width: 18px;
                height: 18px;

                border: 3px solid #ffffff;

                border-radius: 50%;

                background: #22c55e;

                box-shadow:
                    0 2px 9px
                    rgba(34, 197, 94, .45);
            }


            /* =================================================
               ВСПЛЫВАЮЩЕЕ ОКНО
               ================================================= */

            #eruditGreeting {

                position: fixed;

                right: 24px;
                bottom: 112px;

                width: 280px;

                padding: 17px 19px;

                border-radius: 17px;

                border: 1px solid #e1e8ef;

                background: #ffffff;

                color: #173653;

                font-family:
                    -apple-system,
                    BlinkMacSystemFont,
                    "Segoe UI",
                    Roboto,
                    Arial,
                    sans-serif;

                font-size: 14px;

                font-weight: 500;

                line-height: 1.5;

                box-shadow:
                    0 18px 45px
                    rgba(0, 35, 80, .20);

                opacity: 0;

                visibility: hidden;

                transform:
                    translateY(10px);

                transition:
                    opacity .18s ease,
                    transform .18s ease,
                    visibility .18s ease;

                pointer-events: none;
            }


            #eruditGreeting.show {

                opacity: 1;

                visibility: visible;

                transform:
                    translateY(0);
            }


            /* =================================================
               ХВОСТИК
               ================================================= */

            #eruditGreeting::after {

                content: "";

                position: absolute;

                right: 26px;

                bottom: -8px;

                width: 16px;
                height: 16px;

                background: #ffffff;

                border-right:
                    1px solid #e1e8ef;

                border-bottom:
                    1px solid #e1e8ef;

                transform: rotate(45deg);
            }


            /* =================================================
               MOBILE
               ================================================= */

            @media (max-width: 600px) {

                #eruditCircle {

                    right: 14px;
                    bottom: 14px;

                    width: 64px;
                    height: 64px;
                }


                #eruditCircle img {

                    width: 53px;
                    height: 53px;
                }


                #eruditGreeting {

                    right: 14px;
                    bottom: 96px;

                    width:
                        calc(100vw - 28px);

                    max-width: 280px;

                    font-size: 13px;
                }

            }

        </style>


        <a
            id="eruditCircle"
            href="${WELCOME_URL}"
            target="_top"
            aria-label="Открыть Эрудита"
        >

            <img
                src="${AVATAR_URL}"
                alt="Эрудит"
            >

            <span
                id="eruditOnline"
            ></span>

        </a>


        <div
            id="eruditGreeting"
        >
            Привет! Я Эрудит.
            Чем могу помочь?
        </div>

    `;


    // =========================================================
    // ELEMENTS
    // =========================================================

    const circle =
        shadow.getElementById("eruditCircle");

    const greeting =
        shadow.getElementById("eruditGreeting");


    // =========================================================
    // НАВЕДЕНИЕ
    // =========================================================

    circle.addEventListener(
        "mouseenter",
        function () {

            greeting.classList.add("show");

            console.log(
                "✅ ВСПЛЫВАЮЩЕЕ ОКНО ЭРУДИТА ПОКАЗАНО"
            );
        }
    );


    // =========================================================
    // УХОД
    // =========================================================

    circle.addEventListener(
        "mouseleave",
        function () {

            greeting.classList.remove("show");
        }
    );


    console.log(
        "✅ НОВАЯ ВЕРСИЯ ВИДЖЕТА ЭРУДИТА ЗАГРУЖЕНА"
    );

})();